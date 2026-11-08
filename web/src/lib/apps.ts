"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export type SourceType = "github" | "git" | "image";
export type BuildType = "dockerfile" | "compose" | "railpack" | "image";

export interface CurrentDeploy {
  id: number;
  status: string;
  commitSha: string;
  finishedAt: string | null;
}

/** GET /api/apps e /api/apps/{id}. */
export interface AppView {
  id: number;
  projectId: number;
  slug: string;
  sourceType: SourceType;
  repo: string;
  image: string;
  buildType: BuildType;
  branch: string;
  dockerfilePath: string;
  composePath: string;
  composeService: string;
  internalPort: number;
  healthTimeoutS: number;
  autoDeploy: boolean;
  hostPort: number;
  currentDeploy: CurrentDeploy | null;
  domains: string[];
  url: string;
  createdAt: string;
  updatedAt: string;
}

/** Corpo de POST/PUT /api/apps. */
export interface AppInput {
  projectId: number;
  slug: string;
  sourceType: SourceType;
  repo: string;
  image: string;
  buildType: BuildType;
  branch: string;
  dockerfilePath: string;
  composePath: string;
  composeService: string;
  internalPort: number;
  healthTimeoutS: number;
  autoDeploy: boolean;
}

export type AppField =
  | "slug"
  | "repo"
  | "image"
  | "branch"
  | "dockerfilePath"
  | "composePath"
  | "composeService"
  | "internalPort"
  | "healthTimeoutS";

export interface AppFormDefaults {
  /** Campos mostrados no formulário. */
  visible: AppField[];
  /** Campos obrigatórios (os demais têm padrão no servidor). */
  required: AppField[];
  branch: string;
  dockerfilePath: string;
  composePath: string;
}

/** Campos visíveis e obrigatórios do formulário de aplicação. */
export function defaultsFor(sourceType: SourceType, buildType: BuildType): AppFormDefaults {
  const d: AppFormDefaults = {
    visible: ["slug"],
    required: ["slug", "internalPort"],
    branch: "main",
    dockerfilePath: "Dockerfile",
    composePath: "docker-compose.yml",
  };
  if (sourceType === "image") {
    d.visible.push("image");
    d.required.push("image");
  } else {
    d.visible.push("repo", "branch");
    d.required.push("repo", "branch");
    // O caminho do Dockerfile só vale para o build dockerfile (o servidor usa "Dockerfile" quando vem vazio).
    if (buildType === "dockerfile") d.visible.push("dockerfilePath");
    if (buildType === "compose") {
      d.visible.push("composePath", "composeService");
      d.required.push("composeService");
    }
  }
  d.visible.push("internalPort", "healthTimeoutS");
  return d;
}

export function useApps(projectId?: number) {
  return useQuery({
    queryKey: ["apps", { project: projectId ?? null }],
    queryFn: () =>
      api<{ apps: AppView[] }>(`/api/apps${projectId ? `?project=${projectId}` : ""}`).then((r) => r.apps),
  });
}

export function useApp(id: number) {
  return useQuery({
    queryKey: ["apps", id],
    queryFn: () => api<{ app: AppView }>(`/api/apps/${id}`).then((r) => r.app),
  });
}

export function useAppEnv(id: number) {
  return useQuery({
    queryKey: ["apps", id, "env"],
    queryFn: () => api<{ names: string[] }>(`/api/apps/${id}/env`).then((r) => r.names),
  });
}

/** Cria (sem id) ou atualiza (com id) uma aplicação. */
export function useSaveApp() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id?: number; input: AppInput }) =>
      id
        ? api<{ app: AppView }>(`/api/apps/${id}`, { method: "PUT", body: input }).then((r) => r.app)
        : api<{ app: AppView }>("/api/apps", { method: "POST", body: input }).then((r) => r.app),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["apps"] });
      void qc.invalidateQueries({ queryKey: ["projects"] });
    },
  });
}

export function useDeleteApp() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api<void>(`/api/apps/${id}`, { method: "DELETE" }),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["apps"] });
      void qc.invalidateQueries({ queryKey: ["projects"] });
      void qc.invalidateQueries({ queryKey: ["domains"] });
    },
  });
}

/** Grava (`set`) e remove (`remove`) variáveis; valores nunca voltam do servidor. */
export function useSaveEnv(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { set: Record<string, string>; remove: string[] }) =>
      api<void>(`/api/apps/${id}/env`, { method: "PUT", body }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["apps", id, "env"] }),
  });
}
