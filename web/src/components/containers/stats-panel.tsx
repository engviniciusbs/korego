"use client";

import { useId, useState, type ReactNode } from "react";
import { SPARK_SLOTS, Sparkline } from "@/components/charts/sparkline";
import { Alert, Badge, Spinner } from "@/components/ui";
import { cn } from "@/lib/cn";
import type { ContainerStats } from "@/lib/containers";
import { formatBytes, formatPercent } from "@/lib/format";
import { useLiveStream } from "@/lib/ws";

const streamErrors: Record<string, string> = {
  CONTAINER_NOT_FOUND: "O container não existe mais. Volte para a lista de containers.",
  INVALID_CONTAINER_ID: "ID de container inválido.",
};

interface Sample {
  cpuPercent: number;
  memUsedBytes: number;
  memLimitBytes: number;
}

interface State {
  last: Sample | null;
  cpu: number[];
  mem: number[];
  stopped: boolean;
  error: string | null;
}

const push = (arr: number[], v: number) => [...arr.slice(-(SPARK_SLOTS - 1)), v];

function Tile({ title, children, spark, dim }: { title: string; children: ReactNode; spark: ReactNode; dim: boolean }) {
  const id = useId();
  return (
    <section
      aria-labelledby={id}
      className={cn(
        "flex min-w-0 flex-col bg-surface px-4 pt-3 pb-3",
        !dim && "shadow-[inset_2px_0_0_0_var(--color-accent)]",
      )}
    >
      <h2 id={id} className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">
        {title}
      </h2>
      <div className={cn("mt-1.5 min-h-14 flex-1", dim && "opacity-75")}>{children}</div>
      <div className="mt-3">{spark}</div>
    </section>
  );
}

const big = "font-mono text-2xl font-semibold tracking-tight tabular-nums text-fg";
const sub = "mt-0.5 font-mono text-[11px] tabular-nums text-fg-subtle";

/**
 * CPU e RAM do container ao vivo. Assina `container-stats:<id>` enquanto está
 * montado: a página só o monta com a aba Stats aberta.
 */
export function StatsPanel({ containerId }: { containerId: string }) {
  const [s, setS] = useState<State>({ last: null, cpu: [], mem: [], stopped: false, error: null });

  useLiveStream(`container-stats:${containerId}`, (d) => {
    const msg = d as ContainerStats;
    if (msg.error) {
      setS((p) => ({ ...p, error: streamErrors[msg.error!] ?? msg.error! }));
      return;
    }
    // Container parado: o servidor encerra o stream e publica {running:false}.
    if (msg.running === false) {
      setS((p) => ({ ...p, stopped: true }));
      return;
    }
    const sample: Sample = {
      cpuPercent: msg.cpuPercent ?? 0,
      memUsedBytes: msg.memUsedBytes ?? 0,
      memLimitBytes: msg.memLimitBytes ?? 0,
    };
    setS((p) => ({
      last: sample,
      cpu: push(p.cpu, sample.cpuPercent),
      mem: push(p.mem, sample.memUsedBytes),
      stopped: false,
      error: null,
    }));
  });

  if (s.error) {
    return (
      <Alert variant="danger" title="As métricas do container pararam.">
        {s.error}
      </Alert>
    );
  }

  const { last, stopped } = s;
  const memPct = last && last.memLimitBytes > 0 ? (last.memUsedBytes / last.memLimitBytes) * 100 : null;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs text-fg-muted">
          Amostras do Docker enquanto esta aba está aberta; a linha mostra as últimas {SPARK_SLOTS}.
        </p>
        <Badge size="sm" dot variant={stopped ? "neutral" : last ? "success" : "neutral"}>
          {stopped ? "Parado" : last ? "Ao vivo" : "Conectando"}
        </Badge>
      </div>

      <div role="status" aria-live="polite">
        {stopped ? (
          <p className="border border-line border-l-2 border-l-warning bg-warning-bg px-3 py-2 text-[13px] text-fg">
            Container parado
          </p>
        ) : !last ? (
          <p className="flex items-center gap-2 border border-line bg-surface px-3 py-2 text-[13px] text-fg-muted">
            <Spinner size="sm" decorative />
            Aguardando a primeira amostra…
          </p>
        ) : null}
      </div>

      <div className="grid gap-px border border-line bg-line sm:grid-cols-2">
        <Tile
          title="CPU"
          dim={stopped || !last}
          spark={<Sparkline values={s.cpu} max={Math.max(100, ...s.cpu)} />}
        >
          <p className={big}>{last ? formatPercent(last.cpuPercent) : "—"}</p>
          <p className={sub}>pode passar de 100% com vários núcleos</p>
        </Tile>
        <Tile
          title="Memória"
          dim={stopped || !last}
          spark={<Sparkline values={s.mem} max={last?.memLimitBytes || undefined} color="var(--color-series-2)" />}
        >
          <p className={big}>{last ? formatBytes(last.memUsedBytes) : "—"}</p>
          <p className={sub}>
            {last && last.memLimitBytes > 0
              ? `de ${formatBytes(last.memLimitBytes)} · ${formatPercent(memPct ?? 0)}`
              : "usada"}
          </p>
        </Tile>
      </div>
    </div>
  );
}
