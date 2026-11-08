"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export interface GitHubAppInfo {
  slug: string;
  owner: string;
  htmlUrl: string;
  installUrl: string;
}

export interface GitHubInstallation {
  id: number;
  account: string;
}

/** GET /api/github. */
export interface GitHubStatus {
  connected: boolean;
  app: GitHubAppInfo | null;
  installations: GitHubInstallation[];
  installationsError?: string;
  panelReady: boolean;
}

export interface GitHubRepo {
  fullName: string;
  defaultBranch: string;
  installationId: number;
}

export interface ManifestResponse {
  action: string;
  manifest: string;
}

export function useGitHub() {
  return useQuery({
    queryKey: ["github"],
    queryFn: () => api<GitHubStatus>("/api/github"),
  });
}

export function useGitHubRepos(enabled = true) {
  return useQuery({
    queryKey: ["github", "repos"],
    queryFn: () => api<{ repos: GitHubRepo[] }>("/api/github/repos").then((r) => r.repos),
    enabled,
  });
}

/** Pede o manifest ao servidor; quem chama envia com submitManifestForm. */
export function useStartGitHubManifest() {
  return useMutation({
    mutationFn: (org: string) =>
      api<ManifestResponse>("/api/github/manifest", { method: "POST", body: { org } }),
  });
}

export function useDisconnectGitHub() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api<{ removed: boolean; manageUrl: string; message?: string }>("/api/github", { method: "DELETE" }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["github"] }),
  });
}

/**
 * Envia o manifest ao GitHub por um formulário POST (o fluxo do GitHub exige
 * navegação, não fetch). Só aceita action https.
 */
export function submitManifestForm(action: string, manifest: string): void {
  if (!action.startsWith("https://")) {
    throw new Error("Endereço do GitHub inválido: só https é aceito.");
  }
  const form = document.createElement("form");
  form.method = "post";
  form.action = action;
  const input = document.createElement("input");
  input.type = "hidden";
  input.name = "manifest";
  input.value = manifest;
  form.appendChild(input);
  document.body.appendChild(form);
  try {
    form.submit();
  } finally {
    form.remove();
  }
}
