"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export interface Project {
  id: number;
  name: string;
  description: string;
  appCount: number;
  createdAt: string;
}

export interface ProjectInput {
  name: string;
  description: string;
}

export function useProjects() {
  return useQuery({
    queryKey: ["projects"],
    queryFn: () => api<{ projects: Project[] }>("/api/projects").then((r) => r.projects),
  });
}

/** Cria (sem id) ou atualiza (com id) um projeto. */
export function useSaveProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id?: number; input: ProjectInput }) =>
      id
        ? api<{ project: Project }>(`/api/projects/${id}`, { method: "PUT", body: input }).then((r) => r.project)
        : api<{ project: Project }>("/api/projects", { method: "POST", body: input }).then((r) => r.project),
    onSettled: () => qc.invalidateQueries({ queryKey: ["projects"] }),
  });
}

/** Erro PROJECT_NOT_EMPTY quando o projeto ainda tem aplicações. */
export function useDeleteProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api<void>(`/api/projects/${id}`, { method: "DELETE" }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["projects"] }),
  });
}
