"use client";

import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { getLiveSocket } from "./ws";

export type DeployStatus =
  | "queued"
  | "building"
  | "starting"
  | "running"
  | "failed"
  | "superseded"
  | "cancelled";

export interface Deploy {
  id: number;
  appId: number;
  trigger: string;
  status: DeployStatus;
  reason: string;
  commitSha: string;
  commitMessage: string;
  image: string;
  hostPort: number;
  rollbackOf: number | null;
  createdAt: string;
  startedAt: string | null;
  finishedAt: string | null;
}

/** Mensagem do tópico `deploys`. */
export interface DeployEvent {
  id: number;
  appId: number;
  status: DeployStatus;
  reason: string;
}

export interface DeployLogEntry {
  n: number;
  text: string;
}

/** GET /api/deploys/{id}/log?from=N (n > from). */
export interface DeployLogResponse {
  entries: DeployLogEntry[];
  done: boolean;
}

const LABELS: Record<string, string> = {
  queued: "Na fila",
  building: "Construindo",
  starting: "Subindo",
  running: "No ar",
  failed: "Falhou",
  superseded: "Substituído",
  cancelled: "Cancelado",
};

export function statusLabel(status: string): string {
  return LABELS[status] ?? status;
}

/** Deploy ainda em andamento (não terminou). */
export function isActive(status: string): boolean {
  return status === "queued" || status === "building" || status === "starting";
}

const MAX_DEPLOY_LINES = 10000;

/**
 * Buffer do log de um deploy. Junta REST e tópico por `n` (começa em 1):
 * o que já foi visto é ignorado. Uma lacuna (n > último contíguo + 1) para
 * de aceitar linhas e liga `needsRefetch`; refaça a busca com
 * `?from=refetchFrom` e entregue o resultado a setInitial.
 */
export class DeployLogBuffer {
  lines: DeployLogEntry[] = [];
  done = false;
  needsRefetch = false;
  /** Último n contíguo recebido: parâmetro `from` da nova busca. */
  refetchFrom = 0;

  private seen = new Set<number>();
  /** Maior n já descartado pelo limite; nada igual ou abaixo volta. */
  private floor = 0;

  /** Entrega do REST: junta sem detectar lacuna e limpa o pedido de refetch. */
  setInitial(entries: DeployLogEntry[]): void {
    this.needsRefetch = false;
    for (const e of [...entries].sort((a, b) => a.n - b.n)) this.add(e);
    this.finish();
  }

  /** Entrega do tópico `deploy-logs:<id>`. */
  append(entries: DeployLogEntry[]): void {
    for (const e of [...entries].sort((a, b) => a.n - b.n)) {
      if (this.isSeen(e.n)) continue;
      if (e.n > this.refetchFrom + 1) {
        this.needsRefetch = true;
        break;
      }
      this.add(e);
    }
    this.finish();
  }

  markDone(): void {
    this.done = true;
  }

  private isSeen(n: number): boolean {
    return n <= this.floor || this.seen.has(n);
  }

  private add(e: DeployLogEntry): void {
    if (this.isSeen(e.n)) return;
    this.seen.add(e.n);
    this.lines.push({ n: e.n, text: e.text });
    this.advance();
  }

  /** Avança o último n contíguo a partir do que já foi visto. */
  private advance(): void {
    while (this.refetchFrom + 1 <= this.floor || this.seen.has(this.refetchFrom + 1)) this.refetchFrom++;
  }

  private finish(): void {
    this.lines.sort((a, b) => a.n - b.n);
    if (this.lines.length > MAX_DEPLOY_LINES) {
      const drop = this.lines.splice(0, this.lines.length - MAX_DEPLOY_LINES);
      for (const d of drop) this.seen.delete(d.n);
      this.floor = Math.max(this.floor, drop[drop.length - 1].n);
      this.advance();
    }
  }
}

const THROTTLE_MS = 1000;

/** Lista de deploys da aplicação; refaz a busca a cada evento do tópico `deploys` dela (≤ 1 vez por segundo). */
export function useDeploys(appId: number, limit = 20) {
  const qc = useQueryClient();
  const last = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    const fire = () => {
      last.current = Date.now();
      void qc.invalidateQueries({ queryKey: ["deploys", appId] });
      void qc.invalidateQueries({ queryKey: ["apps"] });
    };
    const unsub = getLiveSocket().subscribe("deploys", (d) => {
      if ((d as Partial<DeployEvent> | null)?.appId !== appId) return;
      const wait = THROTTLE_MS - (Date.now() - last.current);
      if (wait <= 0) {
        fire();
      } else if (!timer.current) {
        timer.current = setTimeout(() => {
          timer.current = null;
          fire();
        }, wait);
      }
    });
    return () => {
      unsub();
      if (timer.current) {
        clearTimeout(timer.current);
        timer.current = null;
      }
    };
  }, [qc, appId]);
  return useQuery({
    queryKey: ["deploys", appId, limit],
    queryFn: () =>
      api<{ deploys: Deploy[] }>(`/api/apps/${appId}/deploys?limit=${limit}`).then((r) => r.deploys),
  });
}

export function useDeploy(id: number) {
  return useQuery({
    queryKey: ["deploy", id],
    queryFn: () => api<{ deploy: Deploy }>(`/api/deploys/${id}`).then((r) => r.deploy),
  });
}

/** Busca o log a partir de `from` (exclusivo). */
export function fetchDeployLog(id: number, from = 0): Promise<DeployLogResponse> {
  return api<DeployLogResponse>(`/api/deploys/${id}/log?from=${from}`);
}

function useInvalidateDeploys() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["deploys"] });
    void qc.invalidateQueries({ queryKey: ["deploy"] });
    void qc.invalidateQueries({ queryKey: ["apps"] });
  };
}

export function useStartDeploy() {
  const inv = useInvalidateDeploys();
  return useMutation({
    mutationFn: (appId: number) =>
      api<{ deploy: Deploy }>(`/api/apps/${appId}/deploys`, { method: "POST" }).then((r) => r.deploy),
    onSettled: inv,
  });
}

export function useCancelDeploy() {
  const inv = useInvalidateDeploys();
  return useMutation({
    mutationFn: (deployId: number) => api<void>(`/api/deploys/${deployId}/cancel`, { method: "POST" }),
    onSettled: inv,
  });
}

/** Erros ROLLBACK_NOT_SUPPORTED, IMAGE_GONE e DEPLOY_FINISHED vêm do servidor. */
export function useRollback() {
  const inv = useInvalidateDeploys();
  return useMutation({
    mutationFn: ({ appId, deployId }: { appId: number; deployId: number }) =>
      api<{ deploy: Deploy }>(`/api/apps/${appId}/rollback`, { method: "POST", body: { deployId } }).then(
        (r) => r.deploy,
      ),
    onSettled: inv,
  });
}
