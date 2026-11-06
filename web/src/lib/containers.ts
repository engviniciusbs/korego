"use client";

import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import type { LogLine } from "./logs";
import { getLiveSocket } from "./ws";

export interface PortBinding {
  ip: string;
  privatePort: number;
  publicPort: number;
  proto: string;
}

export interface Container {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
  health: string;
  createdAt: string;
  project: string;
  service: string;
  managed: boolean;
  ports: PortBinding[];
}

export interface Mount {
  type: string;
  source: string;
  destination: string;
  readWrite: boolean;
}

/** GET /api/containers/{id}: `status` vem vazio nos detalhes. */
export interface ContainerDetails extends Container {
  command: string;
  startedAt: string;
  restartCount: number;
  envNames: string[];
  networks: string[];
  mounts: Mount[];
}

/** Mensagem do tópico `container-stats:<id>`. */
export interface ContainerStats {
  cpuPercent?: number;
  memUsedBytes?: number;
  memLimitBytes?: number;
  running?: boolean;
  error?: string;
}

/** GET /api/containers/{id}/logs?tail=N. */
export interface ContainerLogsResponse {
  lines: LogLine[];
}

export type ContainerActionName = "start" | "stop" | "restart" | "remove";

export interface ContainerGroup {
  project: string | null;
  label: string;
  containers: Container[];
}

const byName = (a: Container, b: Container) => a.name.localeCompare(b.name);

/** Agrupa por projeto (ordem alfabética), "Avulsos" por último; containers por nome. */
export function groupContainers(list: Container[]): ContainerGroup[] {
  const projects = new Map<string, Container[]>();
  const loose: Container[] = [];
  for (const c of list) {
    if (!c.project) {
      loose.push(c);
      continue;
    }
    const arr = projects.get(c.project) ?? [];
    arr.push(c);
    projects.set(c.project, arr);
  }
  const groups: ContainerGroup[] = [...projects.keys()]
    .sort((a, b) => a.localeCompare(b))
    .map((p) => ({ project: p, label: p, containers: projects.get(p)!.sort(byName) }));
  if (loose.length > 0) {
    groups.push({ project: null, label: "Avulsos", containers: loose.sort(byName) });
  }
  return groups;
}

/** Remover só é permitido com o container parado. */
export function canRemove(c: Pick<Container, "state">): boolean {
  return c.state === "exited" || c.state === "created" || c.state === "dead";
}

export function useContainers() {
  return useQuery({
    queryKey: ["containers"],
    queryFn: () => api<{ containers: Container[] }>("/api/containers").then((r) => r.containers),
  });
}

export function useContainer(id: string) {
  return useQuery({
    queryKey: ["containers", id],
    queryFn: () => api<ContainerDetails>(`/api/containers/${id}`),
  });
}

export function useContainerAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, action }: { id: string; action: ContainerActionName }) =>
      action === "remove"
        ? api<void>(`/api/containers/${id}`, { method: "DELETE" })
        : api<void>(`/api/containers/${id}/${action}`, { method: "POST" }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["containers"] }),
  });
}

const THROTTLE_MS = 1000;

/** Assina `containers` e invalida ["containers"] no máximo 1 vez por segundo. */
export function useContainersLive(): void {
  const qc = useQueryClient();
  const last = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    const fire = () => {
      last.current = Date.now();
      void qc.invalidateQueries({ queryKey: ["containers"] });
    };
    const unsub = getLiveSocket().subscribe("containers", () => {
      const wait = THROTTLE_MS - (Date.now() - last.current);
      if (wait <= 0) {
        fire();
      } else if (!timer.current) {
        timer.current = setTimeout(() => {
          timer.current = null;
          fire();
        }, wait);
      }
    });
    return () => {
      unsub();
      if (timer.current) {
        clearTimeout(timer.current);
        timer.current = null;
      }
    };
  }, [qc]);
}
