"use client";

import Link from "next/link";
import { formatChartTime } from "@/components/charts/line-chart";
import { PanelState } from "@/components/shell/page-header";
import { Button, Dialog, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import { formatBytes, formatPercent } from "@/lib/format";
import type { MetricsRange } from "@/lib/metrics";
import { isUsageNotReady, kindIcon, useUsageAt, type UsageAtResponse, type UsageMetric } from "@/lib/usage";
import { CPU_COLOR, MEM_COLOR, ownerKindText } from "./owner-row";

export const TOP = 5;

type RankedOwner = Pick<UsageAtResponse["owners"][number], "key" | "kind" | "label" | "cpuPercent" | "memBytes">;

/**
 * Os `n` maiores pela métrica, do maior para o menor (empate pelo nome).
 * "Demais" (`rest`) é a soma de quem ficou fora do histórico, não um dono: fica de fora.
 */
export function topOwners<T extends RankedOwner>(list: T[], metric: UsageMetric, n = TOP): T[] {
  const v = (o: T) => (metric === "cpu" ? o.cpuPercent : o.memBytes);
  return list
    .filter((o) => o.key !== "rest")
    .sort((a, b) => v(b) - v(a) || a.label.localeCompare(b.label, "pt-BR"))
    .slice(0, n);
}

const STEP_TEXT: Record<number, string> = { 60: "1 min", 300: "5 min", 1800: "30 min" };
const stepText = (s: number) => STEP_TEXT[s] ?? `${Math.round(s / 60)} min`;

/**
 * Linha de um dono no ranking: glifo do tipo, nome (link para Consumo), a métrica
 * principal com barra na cor da série e a outra métrica em segundo plano.
 */
export function RankedOwnerRow({
  owner,
  metric,
  memTotal,
}: {
  owner: RankedOwner;
  metric: UsageMetric;
  memTotal?: number;
}) {
  const cpu = formatPercent(owner.cpuPercent);
  const mem = formatBytes(owner.memBytes);
  const pct =
    metric === "cpu" ? owner.cpuPercent : memTotal && memTotal > 0 ? (owner.memBytes / memTotal) * 100 : null;
  const w = pct === null || !Number.isFinite(pct) ? null : Math.min(Math.max(pct, 0), 100);
  return (
    <li className="grid grid-cols-[1.75rem_minmax(0,1fr)_auto] items-center gap-x-3 py-2">
      <span
        aria-hidden="true"
        className="flex h-6 items-center justify-center border border-line font-mono text-[10px] text-fg-subtle"
      >
        {kindIcon(owner.kind)}
      </span>
      <div className="min-w-0">
        <Link
          href="/consumo/"
          translate="no"
          title={owner.label}
          className={cn("block truncate text-[13px] font-medium text-fg hover:text-accent-text hover:underline", focusRing)}
        >
          {owner.label}
        </Link>
        <p className="truncate text-[11px] text-fg-subtle">{ownerKindText(owner)}</p>
      </div>
      <div className="w-24 text-right sm:w-28">
        <p className="font-mono text-[13px] font-semibold tabular-nums text-fg">{metric === "cpu" ? cpu : mem}</p>
        <p className="font-mono text-[10px] tabular-nums text-fg-subtle">
          {metric === "cpu" ? "RAM" : "CPU"} <span className="text-fg-muted">{metric === "cpu" ? mem : cpu}</span>
        </p>
      </div>
      <div aria-hidden="true" className="col-span-2 col-start-2 mt-1.5 h-1 bg-surface-3">
        {w !== null && (
          <div className="h-full" style={{ width: `${w}%`, background: metric === "cpu" ? CPU_COLOR : MEM_COLOR }} />
        )}
      </div>
    </li>
  );
}

export interface WhoWasUsingProps {
  /** Instante clicado no gráfico (unix s); `null` fecha o diálogo. */
  t: number | null;
  /** Período exibido no gráfico: define o tamanho do bucket. */
  range: MetricsRange;
  metric: UsageMetric;
  memTotal?: number;
  onClose: () => void;
}

/** Diálogo "Quem estava usando": os maiores donos no bucket do ponto clicado. */
export function WhoWasUsing({ t, range, metric, memTotal, onClose }: WhoWasUsingProps) {
  return (
    <Dialog
      open={t !== null}
      eyebrow={metric === "cpu" ? "Uso de CPU" : "Memória usada"}
      title="Quem estava usando"
      confirmLabel="Fechar"
      hideCancel
      onConfirm={onClose}
      onCancel={onClose}
    >
      {t !== null && <Body t={t} range={range} metric={metric} memTotal={memTotal} />}
    </Dialog>
  );
}

function Body({ t, range, metric, memTotal }: Omit<WhoWasUsingProps, "onClose"> & { t: number }) {
  const { data, isPending, isError, error, refetch, isFetching } = useUsageAt(t, range, metric);
  const owners = data ? topOwners(data.owners, metric) : [];
  // Antes do primeiro retrato o servidor responde 503: para o usuário, é o mesmo que não ter dados.
  const empty = (data && owners.length === 0) || (isError && isUsageNotReady(error));

  return (
    <>
      <p className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <time
          dateTime={new Date((data?.t ?? t) * 1000).toISOString()}
          className="font-mono text-[13px] tabular-nums text-fg"
        >
          {formatChartTime(data?.t ?? t)}
        </time>
        <span className="text-xs text-fg-subtle">
          {data
            ? `Média de ${stepText(data.stepSeconds)}, por ${metric === "cpu" ? "CPU" : "RAM"}`
            : `Por ${metric === "cpu" ? "CPU" : "RAM"}`}
        </span>
      </p>
      {isPending ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando quem estava usando…</span>
        </PanelState>
      ) : empty ? (
        <PanelState>
          <span role="status">Sem dados para este momento.</span>
        </PanelState>
      ) : isError ? (
        <div role="alert" className="space-y-2 py-2">
          <p className="text-[13px] text-danger">Não foi possível carregar quem estava usando. {error.message}</p>
          <Button size="sm" variant="secondary" loading={isFetching} onClick={() => void refetch()}>
            Tentar novamente
          </Button>
        </div>
      ) : (
        <ol aria-label={`Maiores consumidores por ${metric === "cpu" ? "CPU" : "RAM"}`} className="divide-y divide-line">
          {owners.map((o) => (
            <RankedOwnerRow key={o.key} owner={o} metric={metric} memTotal={memTotal} />
          ))}
        </ol>
      )}
    </>
  );
}
