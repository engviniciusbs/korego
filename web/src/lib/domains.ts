"use client";

import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { getLiveSocket } from "./ws";

export type ProxyState = "active" | "starting" | "blocked" | "trial" | "docker_unavailable" | "error";

/** GET /api/proxy; o tópico `proxy` publica o Status sem `guideUrl`. */
export interface ProxyStatus {
  state: ProxyState;
  process?: string;
  message?: string;
  guideUrl: string;
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

/** Consulta o estado do proxy e aplica os eventos do tópico `proxy` direto nos dados. */
export function useProxyStatus() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["proxy"],
    queryFn: () => api<ProxyStatus>("/api/proxy"),
  });
  useEffect(() => {
    return getLiveSocket().subscribe("proxy", (d) => {
      const s = d as Partial<ProxyStatus> | null;
      if (!s || !s.state) return;
      qc.setQueryData<ProxyStatus>(["proxy"], (old) => ({
        guideUrl: old?.guideUrl ?? "",
        state: s.state!,
        process: s.process,
        message: s.message,
      }));
    });
  }, [qc]);
  return q;
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
