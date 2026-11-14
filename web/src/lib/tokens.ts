"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export interface ApiToken {
  id: number;
  name: string;
  prefix: string;
  createdAt: string;
  lastUsedAt: string | null;
  lastUsedIp: string;
  expiresAt: string | null;
}

export interface CreateTokenInput {
  name: string;
  /** 0 = nunca expira. */
  expiresInDays: 0 | 30 | 90;
  code: string;
}

export interface CreateTokenResult {
  /** Valor em claro; só existe nesta resposta. */
  token: string;
  item: ApiToken;
}

export const tokensKey = ["tokens"] as const;

export function useTokens() {
  return useQuery({ queryKey: tokensKey, queryFn: () => api<{ items: ApiToken[] }>("/api/tokens") });
}

export function useCreateToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateTokenInput) => api<CreateTokenResult>("/api/tokens", { method: "POST", body: input }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: tokensKey });
    },
  });
}

export function useRevokeToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api<void>(`/api/tokens/${id}`, { method: "DELETE" }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: tokensKey });
      await qc.invalidateQueries({ queryKey: ["audit"] });
    },
  });
}

/** Linha para registrar o servidor MCP no Claude Code. */
export function mcpAddCommand(origin: string, token: string): string {
  return `claude mcp add --transport http korego ${origin}/mcp --header "Authorization: Bearer ${token}"`;
}
