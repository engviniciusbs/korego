"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

/** GET /api/system (node.HostInfo). */
export interface HostInfo {
  hostname: string;
  os: string;
  platform: string;
  platformVersion: string;
  kernel: string;
  arch: string;
  cpus: number;
  memoryTotalBytes: number;
  uptimeSeconds: number;
}

export function useSystemInfo() {
  return useQuery({
    queryKey: ["system"],
    queryFn: () => api<HostInfo>("/api/system"),
    staleTime: 60_000,
  });
}
