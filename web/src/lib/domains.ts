"use client";

import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { getLiveSocket } from "./ws";

export type ProxyState = "active" | "starting" | "blocked" | "trial" | "docker_unavailable" | "error";

export type MigrationOutcome = "ok" | "rolled_back" | "failed";

/** Último resultado de assumir/voltar (proxy.last_migration). */
export interface LastMigration {
  op: "takeover" | "rollback";
  outcome: MigrationOutcome;
  message: string;
  at: string;
}

/**
 * GET /api/proxy. O tópico `proxy` publica só o Status do Caddy (state, mode,
 * process, message); os campos de migração vêm apenas do GET.
 */
export interface ProxyStatus {
  state: ProxyState;
  /** normal | trial, quando o proxy roda. */
  mode?: "normal" | "trial";
  process?: string;
  message?: string;
  guideUrl: string;
  /** Servidor anterior ("nginx", "apache2") depois de assumir as portas. */
  migratedFrom?: string | null;
  migratedAt?: string | null;
  lastMigration?: LastMigration | null;
  /** Troca de servidor em andamento: edição de domínios bloqueada. */
  migrating?: boolean;
  rollbackAvailable?: boolean;
}

/** Nome do servidor que ocupa as portas: "nginx (pid 1402)" → "nginx". */
export function processUnit(process?: string): string {
  const name = (process ?? "").trim().split(/\s+/)[0];
  return name || "servidor atual";
}

/** Resultado do ensaio de um caminho (GET /api/proxy/trial-check). */
export interface TrialPathCheck {
  path: string;
  static: boolean;
  ok: boolean;
  status?: number;
  index: boolean;
  error?: string;
}

/** Ensaio de um domínio em 127.0.0.1:18443. */
export interface TrialSiteCheck {
  host: string;
  /** Certificado automático (Let's Encrypt). */
  auto: boolean;
  ok: boolean;
  /** Automático ainda sem certificado: emitido depois de assumir. */
  pending: boolean;
  tlsOk: boolean;
  leafOk: boolean;
  paths: TrialPathCheck[] | null;
  leaf: { names: string[] | null; notAfter?: string; issuer?: string };
  message?: string;
}

export type TrialResult = "ok" | "pending" | "failed";

/** Falha vence; passou com o certificado automático ainda a emitir é pending. */
export function trialResult(s: TrialSiteCheck): TrialResult {
  if (!s.ok) return "failed";
  return s.pending ? "pending" : "ok";
}

/** Motivo da falha de um domínio no ensaio. */
export function trialFailure(s: TrialSiteCheck): string {
  if (s.message) return s.message;
  const p = (s.paths ?? []).find((x) => !x.ok);
  if (p) return `${p.path}: ${p.error || (p.status ? `resposta ${p.status}` : "sem resposta")}`;
  return s.tlsOk ? "falhou" : "falha no TLS";
}

export const TOPIC_MIGRATION = "proxy-migration";

/** Evento do tópico `proxy-migration`; o último tem `done` e `result`. */
export interface MigrationEvent {
  op?: "takeover" | "rollback";
  step?: string;
  status?: "running" | "ok" | "failed";
  message: string;
  done?: boolean;
  result?: MigrationOutcome;
}

export type CertState = "pending" | "active" | "error";

export interface CertInfo {
  state: CertState;
  notAfter?: string;
  issuer?: string;
  message?: string;
}

/**
 * Validade do certificado. O servidor sempre manda `notAfter`; sem certificado
 * vem a data zero do Go ("0001-01-01T00:00:00Z"). Qualquer ano antes de 1970,
 * texto vazio ou inválido conta como ausente.
 */
export function certNotAfter(cert: CertInfo): Date | null {
  if (!cert.notAfter) return null;
  const d = new Date(cert.notAfter);
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 1970) return null;
  return d;
}

export type RouteTarget = "app" | "address" | "static";

/** Rota por caminho de um domínio (GET e corpo de POST/PUT). */
export interface DomainRoute {
  /** "/" ou um prefixo que começa e termina com "/". */
  path: string;
  targetType: RouteTarget;
  appId?: number | null;
  address?: string;
  /** 0 = sem limite. */
  maxBodyBytes: number;
  noBuffering: boolean;
  root?: string;
  spa: boolean;
  immutablePrefix?: string;
}

export type TLSMode = "auto" | "custom";

/** Certificado escolhido para o domínio. */
export interface DomainTLS {
  mode: TLSMode;
  certId: number | null;
  certName?: string;
  notAfter?: string;
  expiresSoon: boolean;
}

export interface DomainHeader {
  name: string;
  value: string;
}

export interface Domain {
  id: number;
  hostname: string;
  /** Espelho da rota "/" (formato antigo). */
  targetType: RouteTarget;
  appId: number | null;
  appSlug: string;
  address: string;
  wwwRedirect: string;
  forceHttps: boolean;
  isPanel: boolean;
  routes: DomainRoute[];
  tls: DomainTLS;
  cloudflareOnly: boolean;
  headers: DomainHeader[];
  compression: boolean;
  cert: CertInfo;
  createdAt: string;
  updatedAt: string;
}

/**
 * Corpo de POST/PUT /api/domains. No PUT, campo ausente mantém o gravado; a
 * interface sempre manda o objeto inteiro. O domínio do painel não manda
 * `routes`, `wwwRedirect` nem `forceHttps`.
 */
export interface DomainInput {
  hostname: string;
  wwwRedirect?: string;
  forceHttps?: boolean;
  routes?: DomainRoute[];
  tls: { mode: TLSMode; certId: number | null };
  cloudflareOnly: boolean;
  headers: DomainHeader[];
  compression: boolean;
}

/** Cabeçalhos do "Modelo segurança", exatamente nesta ordem. */
export const SECURITY_HEADERS: readonly DomainHeader[] = [
  { name: "Strict-Transport-Security", value: "max-age=31536000" },
  { name: "X-Content-Type-Options", value: "nosniff" },
  { name: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { name: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()" },
];

export const MAX_ROUTES = 20;
export const MAX_HEADERS = 30;

/**
 * Rotas do domínio. Respostas antigas (sem `routes`) viram uma rota "/" com o
 * destino do formato antigo.
 */
export function domainRoutes(d: Domain): DomainRoute[] {
  if (d.routes && d.routes.length > 0) return d.routes;
  return [
    {
      path: "/",
      targetType: d.targetType,
      appId: d.appId,
      address: d.address,
      maxBodyBytes: 0,
      noBuffering: false,
      spa: false,
    },
  ];
}

/**
 * Ordem de precedência do proxy: o prefixo mais longo primeiro e "/" por
 * último (empate: ordem alfabética). Caminho vazio (rota nova) fica no topo.
 */
export function byPrecedence(a: string, b: string): number {
  if (a === b) return 0;
  if (a === "") return -1;
  if (b === "") return 1;
  if (a === "/") return 1;
  if (b === "/") return -1;
  return b.length - a.length || a.localeCompare(b);
}

export interface DnsCheck {
  ok: boolean;
  resolved: string[];
  expected: string[];
  message: string;
}

export interface SaveDomainResult {
  domain: Domain;
  dns: DnsCheck;
}

/** Intervalo da releitura de /api/proxy durante uma troca (se o WS cair). */
export const MIGRATING_POLL_MS = 5000;

/**
 * Consulta o estado do proxy e aplica os eventos do tópico `proxy` direto nos
 * dados (mantendo os campos de migração). O fim de uma troca (`proxy-migration`
 * com `done`) relê tudo; durante a troca, relê a cada 5 s.
 */
export function useProxyStatus() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["proxy"],
    queryFn: () => api<ProxyStatus>("/api/proxy"),
    refetchInterval: (query) => (query.state.data?.migrating ? MIGRATING_POLL_MS : false),
  });
  useEffect(() => {
    const live = getLiveSocket();
    const offProxy = live.subscribe("proxy", (d) => {
      const s = d as Partial<ProxyStatus> | null;
      if (!s || !s.state) return;
      qc.setQueryData<ProxyStatus>(["proxy"], (old) => ({
        ...old,
        guideUrl: old?.guideUrl ?? "",
        state: s.state!,
        mode: s.mode,
        process: s.process,
        message: s.message,
      }));
    });
    const offMigration = live.subscribe(TOPIC_MIGRATION, (d) => {
      const e = d as MigrationEvent | null;
      if (!e?.done) return;
      void qc.invalidateQueries({ queryKey: ["proxy"] });
      void qc.invalidateQueries({ queryKey: ["domains"] });
    });
    return () => {
      offProxy();
      offMigration();
    };
  }, [qc]);
  return q;
}

/** Aviso mostrado onde a edição de domínios fica bloqueada durante a troca. */
export const MIGRATING_TITLE = "Troca de servidor em andamento";

/** Edição de domínios bloqueada durante a troca de servidor. */
export function useMigrating(): boolean {
  return Boolean(useProxyStatus().data?.migrating);
}

/** GET /api/proxy/trial-check; o servidor guarda o resultado por até 10 s. */
export function useTrialCheck(enabled: boolean) {
  return useQuery({
    queryKey: ["proxy", "trial-check"],
    queryFn: () => api<{ sites: TrialSiteCheck[] | null }>("/api/proxy/trial-check").then((r) => r.sites ?? []),
    enabled,
    staleTime: 10_000,
  });
}

/** POST /api/proxy/takeover ou /rollback com o código 2FA → 202. */
export function useStartMigration(op: "takeover" | "rollback") {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (code: string) =>
      api<{ status: string; topic: string }>(`/api/proxy/${op}`, { method: "POST", body: { code } }),
    onSuccess: () => {
      qc.setQueryData<ProxyStatus>(["proxy"], (old) => (old ? { ...old, migrating: true } : old));
      void qc.invalidateQueries({ queryKey: ["proxy"], exact: true });
    },
  });
}

export function useRetryProxy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<ProxyStatus>("/api/proxy/retry", { method: "POST" }),
    onSuccess: (s) => qc.setQueryData(["proxy"], s),
    onSettled: () => qc.invalidateQueries({ queryKey: ["domains"] }),
  });
}

export function useDomains() {
  return useQuery({
    queryKey: ["domains"],
    queryFn: () => api<{ domains: Domain[] }>("/api/domains").then((r) => r.domains),
  });
}

/** Cria (sem id) ou atualiza (com id) um domínio. */
export function useSaveDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id?: number; input: DomainInput }) =>
      id
        ? api<SaveDomainResult>(`/api/domains/${id}`, { method: "PUT", body: input })
        : api<SaveDomainResult>("/api/domains", { method: "POST", body: input }),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["domains"] });
      void qc.invalidateQueries({ queryKey: ["apps"] });
      void qc.invalidateQueries({ queryKey: ["panel"] });
      void qc.invalidateQueries({ queryKey: ["certificates"] });
    },
  });
}

export function useDeleteDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api<void>(`/api/domains/${id}`, { method: "DELETE" }),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["domains"] });
      void qc.invalidateQueries({ queryKey: ["apps"] });
      void qc.invalidateQueries({ queryKey: ["panel"] });
      void qc.invalidateQueries({ queryKey: ["certificates"] });
    },
  });
}
