"use client";

import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { getLiveSocket } from "./ws";

export type ProxyState = "active" | "starting" | "blocked" | "docker_unavailable" | "error";

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

export interface Domain {
  id: number;
  hostname: string;
  targetType: "app" | "address";
  appId: number | null;
  appSlug: string;
  address: string;
  wwwRedirect: string;
  forceHttps: boolean;
  isPanel: boolean;
  cert: CertInfo;
  createdAt: string;
  updatedAt: string;
}

/** Corpo de POST/PUT /api/domains. */
export interface DomainInput {
  hostname: string;
  targetType: "app" | "address";
  appId: number | null;
  address: string;
  wwwRedirect: string;
  forceHttps: boolean;
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
    },
  });
}
