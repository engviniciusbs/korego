"use client";

import { useId, useMemo, useState } from "react";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Panel, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { OwnerTable } from "@/components/usage/owner-table";
import { cn } from "@/lib/cn";
import { formatBytes, formatPercent } from "@/lib/format";
import { useMetricsCurrent } from "@/lib/metrics";
import { isUsageNotReady, kindLabel, useUsage, type OwnerKind } from "@/lib/usage";

/** Ordem fixa dos filtros de tipo. */
const KIND_ORDER: OwnerKind[] = ["project", "container", "pm2", "service", "session", "kernel", "other", "rest"];

const timeFormat = new Intl.DateTimeFormat("pt-BR", { hour: "2-digit", minute: "2-digit", second: "2-digit" });

function readTime(iso: string | undefined): string | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : timeFormat.format(d);
}

function KindFilter({
  counts,
  value,
  onChange,
}: {
  counts: Map<string, number>;
  value: string | null;
  onChange: (v: string | null) => void;
}) {
  const total = [...counts.values()].reduce((a, b) => a + b, 0);
  // O tipo escolhido continua visível mesmo se sumir da leitura atual.
  const kinds = KIND_ORDER.filter((k) => counts.has(k) || k === value);
  const items: Array<{ value: string | null; label: string; n: number }> = [
    { value: null, label: "Todos", n: total },
    ...kinds.map((k) => ({ value: k, label: kindLabel(k), n: counts.get(k) ?? 0 })),
  ];
  return (
    <div role="group" aria-label="Filtrar por tipo" className="flex flex-wrap gap-1">
      {items.map((it) => {
        const pressed = it.value === value;
        return (
          <button
            key={it.value ?? "todos"}
            type="button"
            aria-pressed={pressed}
            onClick={() => onChange(it.value)}
            className={cn(
              "inline-flex h-7 touch-manipulation items-center gap-2 border px-2.5 font-mono text-xs",
              "motion-safe:transition-colors motion-safe:duration-100",
              focusRing,
              pressed
                ? "border-accent bg-accent text-accent-fg"
                : "border-line-strong bg-surface text-fg-muted hover:bg-surface-2 hover:text-fg",
            )}
          >
            {it.label}
            <span className={cn("tabular-nums", pressed ? "text-accent-fg/80" : "text-fg-subtle")}>{it.n}</span>
          </button>
        );
      })}
    </div>
  );
}

export default function ConsumoPage() {
  const { data, isPending, isError, error, refetch, isFetching } = useUsage();
  const metrics = useMetricsCurrent();
  const memTotal = metrics.data?.memTotalBytes;
  const [kind, setKind] = useState<string | null>(null);
  const headingId = useId();

  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const o of data?.owners ?? []) m.set(o.kind, (m.get(o.kind) ?? 0) + 1);
    return m;
  }, [data]);

  const visible = useMemo(
    () => (data ? (kind ? data.owners.filter((o) => o.kind === kind) : data.owners) : []),
    [data, kind],
  );

  const totals = useMemo(
    () =>
      (data?.owners ?? []).reduce(
        (acc, o) => ({ cpu: acc.cpu + o.cpuPercent, mem: acc.mem + o.memBytes }),
        { cpu: 0, mem: 0 },
      ),
    [data],
  );

  const notReady = !data && isError && isUsageNotReady(error);
  const time = readTime(data?.time);

  return (
    <>
      <PageHeader
        eyebrow="Servidor"
        title="Consumo"
        description="Quem está usando CPU e RAM, agrupado por dono. A tabela se atualiza a cada 5 s."
        actions={
          data && (
            <Badge size="sm" variant="success" dot>
              {time ? `Ao vivo · ${time}` : "Ao vivo"}
            </Badge>
          )
        }
      />

      {isPending || notReady ? (
        <Panel>
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">
              {notReady
                ? "Coletando os primeiros dados… a primeira leitura leva alguns segundos."
                : "Carregando consumo…"}
            </span>
          </PanelState>
        </Panel>
      ) : !data ? (
        <Alert variant="danger" title="Não foi possível carregar o consumo.">
          <p>{error?.message}</p>
          <Button size="sm" className="mt-2" loading={isFetching} onClick={() => void refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      ) : (
        <div className="space-y-4">
          {!data.dockerOk && (
            <Alert variant="warning" title="Docker indisponível: os containers aparecem em Outros." />
          )}

          <section aria-labelledby={headingId} className="border border-line bg-surface">
            <header className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 border-b border-line px-4 py-3">
              <div className="min-w-0">
                <h2 id={headingId} className="text-sm font-semibold tracking-tight text-fg">
                  Donos
                </h2>
                <p className="mt-0.5 font-mono text-[11px] tabular-nums text-fg-subtle">
                  {data.owners.length} {data.owners.length === 1 ? "dono" : "donos"} · CPU{" "}
                  {formatPercent(Math.min(totals.cpu, 100))} · RAM {formatBytes(totals.mem)}
                  {memTotal ? ` de ${formatBytes(memTotal)}` : ""}
                </p>
              </div>
              <KindFilter counts={counts} value={kind} onChange={setKind} />
            </header>

            {data.owners.length === 0 ? (
              <PanelState>Nenhum processo na leitura atual.</PanelState>
            ) : visible.length === 0 ? (
              <PanelState>
                <span role="status">Nenhum dono do tipo “{kindLabel(kind ?? "")}” agora.</span>
              </PanelState>
            ) : (
              <OwnerTable owners={visible} memTotal={memTotal} />
            )}
          </section>

          <p className="text-xs text-fg-subtle">
            CPU em % da máquina inteira (100% = todos os núcleos). Abra um dono para ver os processos e o histórico.
          </p>
        </div>
      )}
    </>
  );
}
