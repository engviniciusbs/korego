"use client";

import { useState } from "react";
import { keepPreviousData, useQuery, useQueryClient, type Query } from "@tanstack/react-query";
import { api, ApiError } from "./api";
import type { MetricsRange } from "./metrics";
import { useLiveSocketStatus, useLiveStream } from "./ws";

export type OwnerKind = "project" | "container" | "pm2" | "service" | "session" | "kernel" | "other" | "rest";

/** Processo de um dono (GET /api/usage). O comando já vem mascarado do servidor. */
export interface UsageProcess {
  pid: number;
  name: string;
  cmd: string;
  user: string;
  cpuPercent: number;
  memBytes: number;
}

/** Dono no tópico `usage` (sem processos). */
export interface UsageOwnerLive {
  key: string;
  kind: OwnerKind;
  label: string;
  /** Só para containers: gerenciado pelo KoreGO ou externo. */
  managed?: boolean;
  cpuPercent: number;
  memBytes: number;
  procCount: number;
}

/** Dono em GET /api/usage: com até 50 processos, ordenados por CPU. */
export interface UsageOwner extends UsageOwnerLive {
  truncated: boolean;
  processes: UsageProcess[];
}

export interface UsageSnapshot {
  time: string;
  dockerOk: boolean;
  owners: UsageOwner[];
}

/** Mensagem do tópico `usage`. */
export interface UsageLive {
  time: string;
  dockerOk: boolean;
  owners: UsageOwnerLive[];
}

export type UsageMetric = "cpu" | "mem";

/** GET /api/usage/at. */
export interface UsageAtResponse {
  t: number;
  stepSeconds: number;
  metric: UsageMetric;
  owners: Array<Pick<UsageOwnerLive, "key" | "kind" | "label" | "cpuPercent" | "memBytes">>;
}

export interface UsagePoint {
  t: number;
  cpuPercent: number;
  memBytes: number;
}

/** GET /api/usage/history. */
export interface UsageHistoryResponse {
  stepSeconds: number;
  points: UsagePoint[];
}

const KIND_LABELS: Record<OwnerKind, string> = {
  project: "Projeto",
  container: "Container",
  pm2: "PM2",
  service: "Serviço",
  session: "Sessões",
  kernel: "Kernel",
  other: "Outros",
  rest: "Demais",
};

/** Glifos de duas letras: o "ícone" monoespaçado de cada tipo de dono. */
const KIND_ICONS: Record<OwnerKind, string> = {
  project: "PJ",
  container: "CT",
  pm2: "P2",
  service: "SV",
  session: "SS",
  kernel: "KN",
  other: "··",
  rest: "Σ",
};

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind as OwnerKind] ?? kind;
}

export function kindIcon(kind: string): string {
  return KIND_ICONS[kind as OwnerKind] ?? "?";
}

export const isUsageNotReady = (e: unknown) => e instanceof ApiError && e.code === "USAGE_NOT_READY";

/**
 * Aplica uma mensagem do tópico ao retrato atual. O tópico não traz processos:
 * cada dono mantém os processos (e o `truncated`) da última busca REST.
 */
export function mergeUsageLive(prev: UsageSnapshot | undefined, live: UsageLive): UsageSnapshot {
  const old = new Map(prev?.owners.map((o) => [o.key, o]));
  return {
    time: live.time,
    dockerOk: live.dockerOk,
    owners: live.owners.map((o) => {
      const p = old.get(o.key);
      return { ...o, truncated: p?.truncated ?? false, processes: p?.processes ?? [] };
    }),
  };
}

const ms = (iso: string) => {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? -Infinity : t;
};

/**
 * Resolve uma resposta REST que chega depois de uma mensagem mais nova do tópico:
 * fica o retrato mais novo (por `time`), com os processos da resposta REST
 * para os donos que existem nos dois.
 */
export function keepNewerUsage(cached: UsageSnapshot | undefined, fetched: UsageSnapshot): UsageSnapshot {
  if (!cached || ms(fetched.time) >= ms(cached.time)) return fetched;
  const rest = new Map(fetched.owners.map((o) => [o.key, o]));
  return {
    ...cached,
    owners: cached.owners.map((o) => {
      const r = rest.get(o.key);
      return r ? { ...o, truncated: r.truncated, processes: r.processes } : o;
    }),
  };
}

const USAGE_KEY = ["usage", "current"] as const;
const fetchUsage = () => api<UsageSnapshot>("/api/usage");

/** Antes do primeiro retrato (503 USAGE_NOT_READY) tenta de novo a cada 5 s. */
const notReadyInterval = (q: Query<UsageSnapshot, Error>) => (isUsageNotReady(q.state.error) ? 5_000 : false);

/** Retrato atual: semente REST e atualização ao vivo pelo tópico `usage`. */
export function useUsage() {
  const qc = useQueryClient();
  useLiveStream("usage", (d) => {
    qc.setQueryData<UsageSnapshot>(USAGE_KEY, (prev) => mergeUsageLive(prev, d as UsageLive));
  });
  return useQuery({
    queryKey: USAGE_KEY,
    queryFn: async () => {
      const fetched = await fetchUsage();
      // Lê o cache só depois da resposta: uma mensagem do tópico pode ter chegado durante a busca.
      return keepNewerUsage(qc.getQueryData<UsageSnapshot>(USAGE_KEY), fetched);
    },
    retry: (n, e) => !isUsageNotReady(e) && n < 2,
    refetchInterval: notReadyInterval,
  });
}

/**
 * "Ao vivo" só com o WebSocket aberto e depois de ao menos uma mensagem do
 * tópico `usage` nesta conexão; caso contrário a tela mostra um instantâneo.
 */
export function useUsageLive(): boolean {
  const status = useLiveSocketStatus();
  const [got, setGot] = useState(false);
  useLiveStream("usage", () => setGot(true));
  // Ao cair, exige uma nova mensagem antes de voltar a dizer "Ao vivo".
  const [prevStatus, setPrevStatus] = useState(status);
  if (status !== prevStatus) {
    setPrevStatus(status);
    if (status !== "open") setGot(false);
  }
  return status === "open" && got;
}

/** Processos de um dono: busca /api/usage a cada 5 s enquanto `enabled`. */
export function useUsageProcesses(key: string, enabled: boolean) {
  const qc = useQueryClient();
  return useQuery({
    // Uma busca só serve todas as linhas abertas.
    queryKey: ["usage", "processes"],
    queryFn: fetchUsage,
    enabled,
    refetchInterval: enabled ? 5_000 : false,
    // Mostra logo os processos da semente REST, se houver, enquanto a busca chega.
    placeholderData: () => {
      const seed = qc.getQueryData<UsageSnapshot>(USAGE_KEY);
      return seed?.owners.find((o) => o.key === key)?.processes.length ? seed : undefined;
    },
    select: (d) => d.owners.find((o) => o.key === key) ?? null,
  });
}

/** Maiores donos num instante do histórico (painel "Quem estava usando"). */
export function useUsageAt(t: number | null, range: MetricsRange, metric: UsageMetric) {
  return useQuery({
    queryKey: ["usage", "at", t, range, metric],
    queryFn: () => api<UsageAtResponse>(`/api/usage/at?t=${t}&range=${range}&metric=${metric}`),
    enabled: t !== null,
  });
}

/** Histórico de CPU e RAM de um dono. */
export function useUsageHistory(key: string, range: MetricsRange) {
  return useQuery({
    queryKey: ["usage", "history", key, range],
    queryFn: () =>
      api<UsageHistoryResponse>(`/api/usage/history?owner=${encodeURIComponent(key)}&range=${range}`),
    refetchInterval: 60_000,
    placeholderData: keepPreviousData,
  });
}
