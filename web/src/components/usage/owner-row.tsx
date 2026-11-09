"use client";

import { useId, useMemo, useState } from "react";
import { LineChart } from "@/components/charts/line-chart";
import { PanelState } from "@/components/shell/page-header";
import { Badge, Spinner, Tabs, tabId, tabPanelId } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import { formatBytes, formatPercent } from "@/lib/format";
import type { MetricsRange } from "@/lib/metrics";
import { kindIcon, kindLabel, useUsageHistory, useUsageProcesses, type UsageOwnerLive } from "@/lib/usage";

export const CPU_COLOR = "var(--color-series-1)";
export const MEM_COLOR = "var(--color-series-2)";

const RANGES: { value: MetricsRange; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
];
const RANGE_SECONDS: Record<MetricsRange, number> = { "1h": 3600, "24h": 86400, "7d": 7 * 86400 };
/** Passo do bucket → período: 60 s → 1h, 300 s → 24h, 1800 s → 7d. */
const SPAN_BY_STEP: Record<number, number> = { 60: 3600, 300: 86400, 1800: 7 * 86400 };

export const td = "px-3 py-2.5 align-middle";
const subTh = "px-3 py-1.5 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const subTd = "px-3 py-1.5 align-top";

/** Descrição curta abaixo do nome: o tipo, distinguindo projeto compose de container avulso. */
function ownerKindText(o: UsageOwnerLive): string {
  if (o.key.startsWith("project:")) return "Projeto compose";
  if (o.key.startsWith("container:")) return "Container avulso";
  return kindLabel(o.kind);
}

/** Barra horizontal de uso; o valor já aparece em texto ao lado, então é só visual. */
function Bar({ pct, color, kind }: { pct: number | null; color: string; kind: "cpu" | "mem" }) {
  const w = pct === null || !Number.isFinite(pct) ? null : Math.min(Math.max(pct, 0), 100);
  return (
    <div aria-hidden="true" className="h-1 w-full bg-surface-3">
      {w !== null && (
        <div
          data-bar={kind}
          className="h-full"
          style={{ width: `${w}%`, background: color }}
        />
      )}
    </div>
  );
}

function Metric({ text, pct, color, kind }: { text: string; pct: number | null; color: string; kind: "cpu" | "mem" }) {
  return (
    <div className="w-full min-w-28">
      <p className="text-right font-mono text-[13px] tabular-nums text-fg">{text}</p>
      <div className="mt-1.5">
        <Bar pct={pct} color={color} kind={kind} />
      </div>
    </div>
  );
}

function Processes({ owner }: { owner: UsageOwnerLive }) {
  const { data, isPending, isPlaceholderData, isError, error } = useUsageProcesses(owner.key, true);

  if (isPending) {
    return (
      <PanelState>
        <Spinner size="sm" decorative />
        <span role="status">Carregando processos…</span>
      </PanelState>
    );
  }
  if (isError && !data) {
    return <p className="py-4 text-[13px] text-danger">Não foi possível carregar os processos. {error.message}</p>;
  }
  if (!data) {
    return <PanelState>Este dono não aparece na leitura mais recente.</PanelState>;
  }
  if (data.processes.length === 0) {
    return <PanelState>Nenhum processo nesta leitura.</PanelState>;
  }
  return (
    <>
      <div className="relative overflow-x-auto" aria-busy={isPlaceholderData || undefined}>
        <table className="w-full min-w-[36rem] text-[12px]">
          <caption className="sr-only">Processos de {owner.label}</caption>
          <thead>
            <tr>
              <th scope="col" className={cn(subTh, "text-right")}>PID</th>
              <th scope="col" className={subTh}>Nome</th>
              <th scope="col" className={subTh}>Comando</th>
              <th scope="col" className={subTh}>Usuário</th>
              <th scope="col" className={cn(subTh, "text-right")}>CPU</th>
              <th scope="col" className={cn(subTh, "text-right")}>RAM</th>
            </tr>
          </thead>
          <tbody className="font-mono tabular-nums">
            {data.processes.map((p) => (
              <tr key={p.pid} className="border-t border-line/70">
                <td className={cn(subTd, "text-right text-fg-subtle")}>{p.pid}</td>
                <td className={cn(subTd, "max-w-36 truncate text-fg")} translate="no" title={p.name}>
                  {p.name}
                </td>
                <td className={cn(subTd, "min-w-48 text-[11px] text-fg-muted [overflow-wrap:anywhere]")} translate="no">
                  {p.cmd}
                </td>
                <td className={cn(subTd, "max-w-24 truncate text-fg-muted")} translate="no" title={p.user}>
                  {p.user}
                </td>
                <td className={cn(subTd, "text-right text-fg")}>{formatPercent(p.cpuPercent)}</td>
                <td className={cn(subTd, "text-right whitespace-nowrap text-fg")}>{formatBytes(p.memBytes)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {data.truncated && (
        <p className="mt-2 font-mono text-[11px] text-fg-subtle">
          Mostrando os {data.processes.length} processos com mais CPU de {data.procCount}.
        </p>
      )}
    </>
  );
}

function History({ owner, memTotal }: { owner: UsageOwnerLive; memTotal: number | undefined }) {
  const [range, setRange] = useState<MetricsRange>("1h");
  const { data, isPending, isError, error, isPlaceholderData, dataUpdatedAt } = useUsageHistory(owner.key, range);
  const tabsId = useId();

  const series = useMemo(() => {
    const pts = data?.points ?? [];
    return {
      cpu: [{ label: "CPU", points: pts.map((p) => ({ t: p.t, v: p.cpuPercent })), color: CPU_COLOR }],
      mem: [{ label: "RAM", points: pts.map((p) => ({ t: p.t, v: p.memBytes })), color: MEM_COLOR }],
    };
  }, [data]);

  // Mesma janela fixa da Visão geral: ancorada no momento da busca.
  const domain = useMemo<[number, number] | undefined>(() => {
    if (!data || !dataUpdatedAt) return undefined;
    const now = Math.floor(dataUpdatedAt / 1000);
    // A janela segue o passo dos dados exibidos (o período anterior fica na tela até o novo chegar).
    return [now - (SPAN_BY_STEP[data.stepSeconds] ?? RANGE_SECONDS[range]), now];
  }, [data, dataUpdatedAt, range]);

  const step = data?.stepSeconds ?? 60;
  const label = (text: string, color: string) => (
    <p className="mb-1 flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">
      <span aria-hidden="true" className="h-0.5 w-2.5" style={{ background: color }} />
      {text}
    </p>
  );

  return (
    <div>
      <div className="mb-2 flex items-center justify-between gap-3">
        <h3 className="font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-muted">Histórico</h3>
        <div className="flex items-center gap-2">
          {isPlaceholderData && <Spinner size="sm" label="Carregando o novo período…" />}
          <Tabs
            id={tabsId}
            label={`Período do histórico de ${owner.label}`}
            items={RANGES}
            value={range}
            onChange={setRange}
          />
        </div>
      </div>
      <div
        role="tabpanel"
        id={tabPanelId(tabsId)}
        aria-labelledby={tabId(tabsId, range)}
        aria-busy={isPlaceholderData || undefined}
        className={cn("space-y-3 motion-safe:transition-opacity", isPlaceholderData && "opacity-60")}
      >
        {isPending ? (
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">Carregando histórico…</span>
          </PanelState>
        ) : isError && !data ? (
          <p className="py-4 text-[13px] text-danger">Não foi possível carregar o histórico. {error.message}</p>
        ) : (
          <>
            <div>
              {label("CPU", CPU_COLOR)}
              <LineChart series={series.cpu} stepSeconds={step} format={formatPercent} yMax={100} domain={domain} height={72} />
            </div>
            <div>
              {label("RAM", MEM_COLOR)}
              <LineChart series={series.mem} stepSeconds={step} format={formatBytes} yMax={memTotal} domain={domain} height={72} />
            </div>
          </>
        )}
      </div>
    </div>
  );
}

export interface OwnerRowProps {
  owner: UsageOwnerLive;
  memTotal: number | undefined;
  expanded: boolean;
  onToggle: () => void;
}

/** Linha de um dono e, quando aberta, a linha de detalhe com processos e histórico. */
export function OwnerRow({ owner: o, memTotal, expanded, onToggle }: OwnerRowProps) {
  const detailId = useId();
  const memPct = memTotal && memTotal > 0 ? (o.memBytes / memTotal) * 100 : null;
  return (
    <>
      <tr
        className={cn(
          "border-t border-line motion-safe:transition-colors hover:bg-surface-2/60",
          expanded && "bg-surface-2/60 shadow-[inset_2px_0_0_0_var(--color-accent)]",
        )}
      >
        <td className={cn(td, "min-w-56")}>
          <div className="flex items-center gap-3">
            <span
              aria-hidden="true"
              title={kindLabel(o.kind)}
              className="flex size-7 shrink-0 items-center justify-center border border-line-strong bg-surface-2 font-mono text-[10px] font-semibold tracking-tight text-fg-muted"
            >
              {kindIcon(o.kind)}
            </span>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <button
                  type="button"
                  aria-expanded={expanded}
                  aria-controls={detailId}
                  onClick={onToggle}
                  translate="no"
                  className={cn(
                    "group inline-flex min-h-6 touch-manipulation items-center gap-2 text-left font-mono text-[13px] font-medium text-fg [overflow-wrap:anywhere] hover:text-accent-text",
                    focusRing,
                  )}
                >
                  <span
                    aria-hidden="true"
                    className={cn(
                      "size-1.5 shrink-0 border-r-[1.5px] border-b-[1.5px] border-current text-fg-subtle group-hover:text-accent-text motion-safe:transition-transform",
                      expanded ? "rotate-45 -translate-y-0.5" : "-rotate-45",
                    )}
                  />
                  {o.label}
                </button>
                {o.managed !== undefined && (
                  <Badge size="sm" variant={o.managed ? "accent" : "neutral"}>
                    {o.managed ? "gerenciado" : "externo"}
                  </Badge>
                )}
              </div>
              <p className="mt-0.5 pl-3.5 text-[11px] text-fg-subtle">{ownerKindText(o)}</p>
            </div>
          </div>
        </td>
        <td className={td}>
          <Metric text={formatPercent(o.cpuPercent)} pct={o.cpuPercent} color={CPU_COLOR} kind="cpu" />
        </td>
        <td className={td}>
          <Metric text={formatBytes(o.memBytes)} pct={memPct} color={MEM_COLOR} kind="mem" />
        </td>
        <td className={cn(td, "text-right font-mono text-[13px] tabular-nums text-fg-muted")}>{o.procCount}</td>
      </tr>
      <tr id={detailId} hidden={!expanded} className="bg-bg/40">
        {expanded && (
          <td colSpan={4} className="border-t border-line px-3 pt-3 pb-4 shadow-[inset_2px_0_0_0_var(--color-accent)]">
            <div className="grid gap-5 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
              <div className="min-w-0">
                <h3 className="mb-2 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-muted">
                  Processos
                </h3>
                <Processes owner={o} />
              </div>
              <div className="min-w-0">
                <History owner={o} memTotal={memTotal} />
              </div>
            </div>
          </td>
        )}
      </tr>
    </>
  );
}
