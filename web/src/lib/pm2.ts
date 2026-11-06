"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export interface PM2Process {
  name: string;
  pmId: number;
  status: string;
  cpuPercent: number;
  memoryBytes: number;
  restarts: number;
  cwd: string;
  uptimeSeconds: number;
}

/** GET /api/pm2. */
export interface PM2Snapshot {
  available: boolean;
  daemonRunning: boolean;
  processes: PM2Process[];
}

export function usePM2() {
  return useQuery({
    queryKey: ["pm2"],
    queryFn: () => api<PM2Snapshot>("/api/pm2"),
    refetchInterval: 10_000,
  });
}
