"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export interface DiskUsage {
  mount: string;
  fstype: string;
  usedBytes: number;
  totalBytes: number;
}

/** GET /api/metrics/current e tópico `metrics`. */
export interface Sample {
  time: string;
  cpuPercent: number;
  memUsedBytes: number;
  memTotalBytes: number;
  load1: number;
  load5: number;
  load15: number;
  netRxBytesPerSec: number;
  netTxBytesPerSec: number;
  disks: DiskUsage[];
}

export type MetricsRange = "1h" | "24h" | "7d";

export interface HistoryPoint {
  t: number;
  cpuPercent: number;
  memUsedBytes: number;
  memTotalBytes: number;
  load1: number;
  netRxBytesPerSec: number;
  netTxBytesPerSec: number;
  diskUsedBytes: number;
  diskTotalBytes: number;
}

export interface HistoryResponse {
  range: MetricsRange;
  stepSeconds: number;
  points: HistoryPoint[];
}

export function useMetricsCurrent() {
  return useQuery({
    queryKey: ["metrics", "current"],
    queryFn: () => api<Sample>("/api/metrics/current"),
  });
}

export function useMetricsHistory(range: MetricsRange) {
  return useQuery({
    queryKey: ["metrics", "history", range],
    queryFn: () => api<HistoryResponse>(`/api/metrics/history?range=${range}`),
    refetchInterval: 60_000,
  });
}
