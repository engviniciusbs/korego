"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";
import { api } from "./api";

const NEXT_RE = /^\/autorizar\/\?req=[A-Za-z0-9_-]{43}$/;

/**
 * Valida o retorno pós-login (`?next=`). Só a tela de autorização com um id de
 * pedido bem formado passa; qualquer outra coisa (URL externa, `//host`, barra
 * invertida, caracteres de controle) vira null e o login cai em "/".
 */
export function safeNext(next: string | null): string | null {
  if (next === null || !NEXT_RE.test(next)) return null;
  return next;
}

const subscribe = () => () => {};

/** `window.location.search` sem useSearchParams (que exigiria Suspense no export estático). */
export function useLocationSearch(): string {
  return useSyncExternalStore(
    subscribe,
    () => window.location.search,
    () => "",
  );
}

export interface OAuthRequestInfo {
  clientName: string;
  destination: string;
  registeredAt: string;
  sessionFresh: boolean;
}

export interface ApproveOAuthInput {
  code: string;
  ttlDays: 30 | 90;
}

const requestPath = (id: string) => `/api/oauth/requests/${encodeURIComponent(id)}`;

export function useOAuthRequest(id: string) {
  return useQuery({
    queryKey: ["oauth-request", id],
    queryFn: () => api<OAuthRequestInfo>(requestPath(id)),
    enabled: id !== "",
    retry: false,
    staleTime: Infinity,
  });
}

export function useApproveOAuth(id: string) {
  return useMutation({
    gcTime: 0,
    mutationFn: (input: ApproveOAuthInput) =>
      api<{ redirect: string }>(`${requestPath(id)}/approve`, { method: "POST", body: input }),
  });
}

export function useDenyOAuth(id: string) {
  return useMutation({
    gcTime: 0,
    mutationFn: () => api<{ redirect: string }>(`${requestPath(id)}/deny`, { method: "POST" }),
  });
}
