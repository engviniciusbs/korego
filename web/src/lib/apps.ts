"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export type SourceType = "github" | "git" | "image" | "local";
export type BuildType = "dockerfile" | "compose" | "railpack" | "image" | "static" | "compose-local";
export type UpdateMode = "pull" | "script";

/** Padrões do servidor para apps estáticas (valores vazios caem neles). */
export const STATIC_DEFAULTS = {
  outputDir: "dist",
  baseDir: ".",
  image: "node:22-alpine",
  immutablePrefix: "/assets/",
} as const;

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
  staticBuildCmd: string;
  staticOutputDir: string;
  staticBaseDir: string;
  staticImage: string;
  staticSpa: boolean;
  staticImmutablePrefix: string;
  /** Só saída: raiz de publicação de apps estáticas e compose-local. */
  publishRoot: string;
  /** compose-local: projeto do docker compose (não muda depois da adoção). */
  composeProject: string;
  /** compose-local: "pull" (pull + up -d) ou "script". */
  updateMode: UpdateMode | "";
  /** compose-local, modo script: caminho relativo à pasta da stack. */
  updateScript: string;
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
  staticBuildCmd: string;
  staticOutputDir: string;
  staticBaseDir: string;
  staticImage: string;
  staticSpa: boolean;
  staticImmutablePrefix: string;
  /** Só compose-local. */
  updateMode?: UpdateMode;
  updateScript?: string;
  /** Código 2FA (sessão) ao escolher ou mudar o modo script. */
  code?: string;
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
  | "healthTimeoutS"
  | "staticBuildCmd"
  | "staticOutputDir"
  | "staticBaseDir"
  | "staticImage"
  | "staticImmutablePrefix";

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
  if (buildType === "static" && sourceType !== "image") {
    // Sem porta nem health check: o nginx do proxy serve a raiz publicada.
    d.required = ["slug", "repo", "branch"];
    d.visible.push(
      "repo",
      "branch",
      "staticBuildCmd",
      "staticOutputDir",
      "staticBaseDir",
      "staticImage",
      "staticImmutablePrefix",
    );
    return d;
  }
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

/** Corpo do PUT que regrava a app como está, com as mudanças de `patch`. */
export function appInputOf(app: AppView, patch: Partial<AppInput> = {}): AppInput {
  const local = app.sourceType === "local";
  return {
    projectId: app.projectId,
    slug: app.slug,
    sourceType: app.sourceType,
    repo: app.repo,
    image: app.image,
    buildType: app.buildType,
    branch: app.branch,
    dockerfilePath: app.dockerfilePath,
    composePath: app.composePath,
    composeService: app.composeService,
    internalPort: app.internalPort,
    healthTimeoutS: app.healthTimeoutS,
    autoDeploy: app.autoDeploy,
    staticBuildCmd: app.staticBuildCmd,
    staticOutputDir: app.staticOutputDir,
    staticBaseDir: app.staticBaseDir,
    staticImage: app.staticImage,
    staticSpa: app.staticSpa,
    staticImmutablePrefix: app.staticImmutablePrefix,
    ...(local ? { updateMode: app.updateMode || "pull", updateScript: app.updateScript } : {}),
    ...patch,
  };
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
