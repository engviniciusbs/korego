"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import type { AppView, UpdateMode } from "./apps";

/** Corpo de POST /api/adopt/static. `current` é o link servido hoje (ex.: /var/www/site/current). */
export interface AdoptStaticInput {
  projectId: number;
  slug: string;
  current: string;
  sourceType: "github" | "git";
  repo: string;
  branch: string;
  staticBuildCmd: string;
  staticOutputDir: string;
  staticBaseDir: string;
  staticImage: string;
  staticSpa?: boolean;
  staticImmutablePrefix: string;
}

/** Corpo de POST /api/adopt/compose; `code` (2FA) só no modo script. */
export interface AdoptComposeInput {
  projectId: number;
  slug: string;
  composeProject: string;
  updateMode: UpdateMode;
  updateScript?: string;
  code?: string;
}

/** GET /api/apps/{id}/stack: lido na hora dos rótulos do compose. */
export interface StackInfo {
  project: string;
  dir: string;
  files: string[];
  envPath: string;
}

export type StackOp = "start" | "stop" | "restart";

/** Slug a partir de um hostname ou projeto compose: minúsculas, números e hífen, até 63. */
export function suggestSlug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+/, "")
    .slice(0, 63)
    .replace(/-+$/, "");
}

function useInvalidateAdopted() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["apps"] });
    void qc.invalidateQueries({ queryKey: ["projects"] });
    void qc.invalidateQueries({ queryKey: ["domains"] });
    void qc.invalidateQueries({ queryKey: ["containers"] });
  };
}

export function useAdoptStatic() {
  const inv = useInvalidateAdopted();
  return useMutation({
    mutationFn: (input: AdoptStaticInput) =>
      api<{ app: AppView; domains: string[] }>("/api/adopt/static", { method: "POST", body: input }),
    onSettled: inv,
  });
}

export function useAdoptCompose() {
  const inv = useInvalidateAdopted();
  return useMutation({
    mutationFn: (input: AdoptComposeInput) =>
      api<{ app: AppView }>("/api/adopt/compose", { method: "POST", body: input }).then((r) => r.app),
    onSettled: inv,
  });
}

export function useStackInfo(appId: number, enabled = true) {
  return useQuery({
    queryKey: ["apps", appId, "stack"],
    queryFn: () => api<StackInfo>(`/api/apps/${appId}/stack`),
    enabled,
  });
}

/** Iniciar, parar (com `code`) ou reiniciar a stack. */
export function useStackAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ appId, op, code }: { appId: number; op: StackOp; code?: string }) =>
      api<void>(`/api/apps/${appId}/stack/${op}`, { method: "POST", body: code ? { code } : undefined }),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["containers"] });
    },
  });
}
