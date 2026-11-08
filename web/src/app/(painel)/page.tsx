"use client";

import Link from "next/link";
import { useId, useMemo, useState, type ReactNode } from "react";
import { LineChart } from "@/components/charts/line-chart";
import { Sparkline } from "@/components/charts/sparkline";
import { ProxyBanner } from "@/components/domains/proxy-banner";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Panel, Spinner, Tabs, tabId, tabPanelId, type BadgeVariant } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { useContainers, useContainersLive } from "@/lib/containers";
import { formatBytes, formatGiB, formatPercent, formatRate, formatUptime } from "@/lib/format";
import { useMetricsCurrent, useMetricsHistory, type MetricsRange, type Sample } from "@/lib/metrics";
import { usePM2 } from "@/lib/pm2";
import { usePanelSettings } from "@/lib/settings";
import { useSystemInfo } from "@/lib/system";
import { useLive, useLiveStream } from "@/lib/ws";

const loadFormat = new Intl.NumberFormat("pt-BR", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const formatLoad = (v: number) => loadFormat.format(Number.isFinite(v) ? v : 0);

function Fact({ label, children }: { label: string; children: ReactNode }) {
  const text = typeof children === "string" || typeof children === "number" ? String(children) : undefined;
  return (
    <div className="min-w-0 bg-surface px-4 py-3">
      <dt className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">{label}</dt>
      <dd className="mt-1 truncate font-mono text-[13px] text-fg" translate="no" title={text}>
        {children}
      </dd>
    </div>
  );
}

function SectionHeading({ id, title, children }: { id: string; title: string; children?: ReactNode }) {
  return (
    <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
      <h2 id={id} className="font-mono text-[11px] font-medium uppercase tracking-[0.12em] text-fg-muted">
        {title}
      </h2>
      {children}
    </div>
  );
}

// --- Servidor (etapa 0) -------------------------------------------------------

function ServerSection() {
  const { data, isPending, isError, error, refetch, isFetching } = useSystemInfo();
  const live = useLive<{ uptimeSeconds: number }>("system");
  const uptime = live?.uptimeSeconds ?? data?.uptimeSeconds;

  if (isPending) {
    return (
      <Panel title="Servidor">
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando informações do servidor…</span>
        </PanelState>
      </Panel>
    );
  }
  if (isError) {
    return (
      <Alert variant="danger" title="Não foi possível carregar as informações do servidor.">
        <p>{error.message}</p>
        <Button size="sm" className="mt-2" loading={isFetching} onClick={() => void refetch()}>
          Tentar novamente
        </Button>
      </Alert>
    );
  }
  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
      <Panel title="Servidor" variant="accent">
        <dl className="-m-4 grid grid-cols-1 gap-px bg-line sm:grid-cols-2 xl:grid-cols-3">
          <Fact label="Hostname">{data.hostname}</Fact>
          <Fact label="Sistema">{[data.platform || data.os, data.platformVersion].filter(Boolean).join(" ")}</Fact>
          <Fact label="Kernel">{data.kernel || "—"}</Fact>
          <Fact label="Arquitetura">{data.arch}</Fact>
          <Fact label="CPUs">{data.cpus}</Fact>
          <Fact label="Memória total">{formatGiB(data.memoryTotalBytes)}</Fact>
        </dl>
      </Panel>
      <Panel
        title="Tempo ligado"
        actions={
          <Badge size="sm" variant={live ? "success" : "neutral"} dot>
            {live ? "Ao vivo" : "Instantâneo"}
          </Badge>
        }
      >
        <p className="font-mono text-3xl font-semibold tracking-tight tabular-nums text-fg">
          {uptime === undefined ? "—" : formatUptime(uptime)}
        </p>
        <p className="mt-2 text-xs text-fg-subtle">Desde a última inicialização do sistema.</p>
      </Panel>
    </div>
  );
}

// --- Ao vivo -----------------------------------------------------------------

interface LiveHistory {
  cpu: number[];
  mem: number[];
  load: number[];
  rx: number[];
  tx: number[];
}

const KEEP = 60; // 60 amostras × 2 s = 2 minutos
const push = (arr: number[], v: number) => [...arr.slice(-(KEEP - 1)), v];

/** Última amostra do canal `metrics` e os últimos 60 valores de cada métrica. */
function useLiveMetrics(initial: Sample | undefined) {
  const [state, setState] = useState<{ sample: Sample; hist: LiveHistory } | null>(null);

  useLiveStream("metrics", (d) => {
    const s = d as Sample;
    setState((prev) => {
      const seed = initial; // useLiveStream sempre chama a versão mais recente do callback
      const base: LiveHistory = prev?.hist ?? {
        cpu: seed ? [seed.cpuPercent] : [],
        mem: seed ? [seed.memUsedBytes] : [],
        load: seed ? [seed.load1] : [],
        rx: seed ? [seed.netRxBytesPerSec] : [],
        tx: seed ? [seed.netTxBytesPerSec] : [],
      };
      return {
        sample: s,
        hist: {
          cpu: push(base.cpu, s.cpuPercent),
          mem: push(base.mem, s.memUsedBytes),
          load: push(base.load, s.load1),
          rx: push(base.rx, s.netRxBytesPerSec),
          tx: push(base.tx, s.netTxBytesPerSec),
        },
      };
    });
  });

  return state;
}

function StatTile({
  title,
  live,
  children,
  spark,
}: {
  title: string;
  live: boolean;
  children: ReactNode;
  spark: ReactNode;
}) {
  const id = useId();
  return (
    <section
      aria-labelledby={id}
      className={cn(
        "flex min-w-0 flex-col border border-line bg-surface px-4 pt-3 pb-3",
        live && "shadow-[inset_2px_0_0_0_var(--color-accent)]",
      )}
    >
      <h3 id={id} className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">
        {title}
      </h3>
      <div className="mt-1.5 min-h-14 flex-1">{children}</div>
      <div className="mt-3">{spark}</div>
    </section>
  );
}

const big = "font-mono text-2xl font-semibold tracking-tight tabular-nums text-fg";
const sub = "mt-0.5 font-mono text-[11px] tabular-nums text-fg-subtle";

function KeyValue({ label, value, color }: { label: string; value: string; color: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="flex items-center gap-1.5 text-[11px] text-fg-muted">
        <span aria-hidden="true" className="h-0.5 w-2.5" style={{ background: color }} />
        {label}
      </span>
      <span className="font-mono text-base font-semibold tabular-nums text-fg">{value}</span>
    </div>
  );
}

function LiveSection({ sample, hist }: { sample: Sample | undefined; hist: LiveHistory | null }) {
  const live = hist !== null;
  const h = hist ?? { cpu: [], mem: [], load: [], rx: [], tx: [] };
  const netMax = Math.max(...h.rx, ...h.tx, 0) || undefined;
  const dash = "—";
  return (
    <div className="grid gap-px border border-line bg-line sm:grid-cols-2 xl:grid-cols-4 [&>section]:border-0">
      <StatTile title="CPU" live={live} spark={<Sparkline values={h.cpu} max={100} />}>
        <p className={big}>{sample ? formatPercent(sample.cpuPercent) : dash}</p>
        <p className={sub}>uso total</p>
      </StatTile>
      <StatTile title="Memória" live={live} spark={<Sparkline values={h.mem} max={sample?.memTotalBytes} />}>
        <p className={big}>{sample ? formatBytes(sample.memUsedBytes) : dash}</p>
        <p className={sub}>
          {sample
            ? `de ${formatBytes(sample.memTotalBytes)} · ${formatPercent(
                sample.memTotalBytes > 0 ? (sample.memUsedBytes / sample.memTotalBytes) * 100 : 0,
              )}`
            : "usada"}
        </p>
      </StatTile>
      <StatTile title="Load" live={live} spark={<Sparkline values={h.load} />}>
        <dl className="grid grid-cols-3 gap-2">
          {(
            [
              ["1 min", sample?.load1],
              ["5 min", sample?.load5],
              ["15 min", sample?.load15],
            ] as const
          ).map(([label, v], i) => (
            <div key={label} className="min-w-0">
              <dt className="font-mono text-[10px] text-fg-subtle">{label}</dt>
              <dd className={cn("font-mono font-semibold tabular-nums text-fg", i === 0 ? "text-2xl tracking-tight" : "pt-1.5 text-base text-fg-muted")}>
                {v === undefined ? dash : formatLoad(v)}
              </dd>
            </div>
          ))}
        </dl>
      </StatTile>
      <StatTile
        title="Rede"
        live={live}
        spark={
          <div className="relative">
            <Sparkline values={h.rx} max={netMax} color="var(--color-series-1)" />
            <Sparkline values={h.tx} max={netMax} color="var(--color-series-2)" className="absolute inset-0" />
          </div>
        }
      >
        <div className="space-y-1">
          <KeyValue label="Entrada" value={sample ? formatRate(sample.netRxBytesPerSec) : dash} color="var(--color-series-1)" />
          <KeyValue label="Saída" value={sample ? formatRate(sample.netTxBytesPerSec) : dash} color="var(--color-series-2)" />
        </div>
      </StatTile>
    </div>
  );
}

// --- Disco -------------------------------------------------------------------

function DiskPanel({ sample, pending }: { sample: Sample | undefined; pending: boolean }) {
  const disks = sample?.disks ?? [];
  return (
    <Panel title="Disco" description="Uso por montagem." headingLevel={3}>
      {pending && !sample ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando métricas…</span>
        </PanelState>
      ) : disks.length === 0 ? (
        <PanelState>Nenhuma montagem encontrada.</PanelState>
      ) : (
        <ul className="-my-1 divide-y divide-line">
          {disks.map((d) => {
            const pct = d.totalBytes > 0 ? (d.usedBytes / d.totalBytes) * 100 : 0;
            const level = pct >= 90 ? "danger" : pct >= 80 ? "warning" : null;
            return (
              <li key={d.mount} className="grid grid-cols-[minmax(0,1fr)_3.5rem] items-center gap-x-4 gap-y-1.5 py-2.5">
                <div className="col-span-2 flex min-w-0 items-baseline justify-between gap-4">
                  <div className="flex min-w-0 items-baseline gap-2">
                    <span className="truncate font-mono text-[13px] text-fg" translate="no" title={d.mount}>
                      {d.mount}
                    </span>
                    <span className="shrink-0 font-mono text-[10px] text-fg-subtle">{d.fstype}</span>
                    {level && (
                      <Badge size="sm" variant={level}>
                        {level === "danger" ? "Quase cheio" : "Atenção"}
                      </Badge>
                    )}
                  </div>
                  <span className="shrink-0 font-mono text-[11px] tabular-nums text-fg-muted">
                    {formatBytes(d.usedBytes)} de {formatBytes(d.totalBytes)}
                  </span>
                </div>
                <div
                  role="meter"
                  aria-label={`Uso de ${d.mount}`}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={Math.round(pct)}
                  aria-valuetext={formatPercent(pct)}
                  className="h-1.5 bg-surface-3"
                >
                  <div
                    className={cn(
                      "h-full",
                      level === "danger" ? "bg-danger" : level === "warning" ? "bg-warning" : "bg-accent",
                    )}
                    style={{ width: `${Math.min(pct, 100)}%` }}
                  />
                </div>
                <span className="text-right font-mono text-[11px] tabular-nums text-fg">{formatPercent(pct)}</span>
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}

// --- Containers ---------------------------------------------------------------

function ContainersPanel() {
  const { data, isPending, isError, error, refetch, isFetching } = useContainers();
  useContainersLive();
  const unavailable =
    isError && error instanceof ApiError && (error.code === "DOCKER_UNAVAILABLE" || error.status === 503);
  const running = data?.filter((c) => c.state === "running").length ?? 0;
  const total = data?.length ?? 0;

  return (
    <Panel
      title="Containers"
      headingLevel={3}
      actions={
        <Link
          href="/containers/"
          className={cn("text-xs text-accent-text hover:text-fg hover:underline", focusRing)}
        >
          Ver containers <span aria-hidden="true">→</span>
        </Link>
      }
    >
      {isPending ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Consultando o Docker…</span>
        </PanelState>
      ) : isError ? (
        <div className="space-y-3">
          <p className="flex items-center gap-2 text-[13px] font-medium text-warning">
            <span aria-hidden="true" className="size-1.5 bg-current" />
            {unavailable ? "Docker indisponível" : "Não foi possível listar os containers."}
          </p>
          {!unavailable && <p className="text-xs text-fg-muted">{error.message}</p>}
          <Button size="sm" loading={isFetching} onClick={() => void refetch()}>
            Tentar novamente
          </Button>
        </div>
      ) : (
        <div>
          <p className="font-mono text-[15px] font-semibold tabular-nums text-fg">
            {`Containers: ${running} rodando de ${total}`}
          </p>
          <div aria-hidden="true" className="mt-3 flex h-1.5 gap-0.5 bg-surface-3">
            {total > 0 && <div className="bg-success" style={{ width: `${(running / total) * 100}%` }} />}
          </div>
          <p className="mt-2 text-xs text-fg-subtle">
            {total - running === 0 ? "Nenhum parado." : `${total - running} parado${total - running > 1 ? "s" : ""}.`}
          </p>
        </div>
      )}
    </Panel>
  );
}

// --- Histórico ---------------------------------------------------------------

const RANGES: { value: MetricsRange; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
];

const RANGE_SECONDS: Record<MetricsRange, number> = { "1h": 3600, "24h": 86400, "7d": 7 * 86400 };

function HistorySection({ memTotal }: { memTotal: number | undefined }) {
  const [range, setRange] = useState<MetricsRange>("1h");
  const { data, isPending, isError, error, refetch, isFetching, isPlaceholderData, dataUpdatedAt } =
    useMetricsHistory(range);
  const headingId = useId();
  const tabsId = useId();

  const series = useMemo(() => {
    const pts = data?.points ?? [];
    return {
      cpu: [{ label: "CPU", points: pts.map((p) => ({ t: p.t, v: p.cpuPercent })) }],
      mem: [{ label: "RAM usada", points: pts.map((p) => ({ t: p.t, v: p.memUsedBytes })) }],
      net: [
        { label: "Entrada", points: pts.map((p) => ({ t: p.t, v: p.netRxBytesPerSec })) },
        { label: "Saída", points: pts.map((p) => ({ t: p.t, v: p.netTxBytesPerSec })) },
      ],
      memTotal: pts.length ? pts[pts.length - 1].memTotalBytes : memTotal,
    };
  }, [data, memTotal]);

  // Janela fixa do período dos dados exibidos, ancorada no momento da busca:
  // buracos no começo e no fim aparecem e o eixo não se mexe a cada render.
  const domain = useMemo<[number, number] | undefined>(() => {
    if (!data || !dataUpdatedAt) return undefined;
    const now = Math.floor(dataUpdatedAt / 1000);
    return [now - RANGE_SECONDS[data.range], now];
  }, [data, dataUpdatedAt]);

  const step = data?.stepSeconds ?? 60;
  const body = (chart: ReactNode) =>
    isPending ? (
      <PanelState>
        <Spinner size="sm" decorative />
        <span role="status">Carregando histórico…</span>
      </PanelState>
    ) : (
      chart
    );

  return (
    <section aria-labelledby={headingId}>
      <SectionHeading id={headingId} title="Histórico">
        <div className="flex items-center gap-3">
          {isPlaceholderData && <Spinner size="sm" label="Carregando o novo período…" />}
          <Tabs id={tabsId} label="Período do histórico" items={RANGES} value={range} onChange={setRange} />
        </div>
      </SectionHeading>
      <div role="tabpanel" id={tabPanelId(tabsId)} aria-labelledby={tabId(tabsId, range)}>
        {isError && !data ? (
          <Alert variant="danger" title="Não foi possível carregar o histórico.">
            <p>{error.message}</p>
            <Button size="sm" className="mt-2" loading={isFetching} onClick={() => void refetch()}>
              Tentar novamente
            </Button>
          </Alert>
        ) : (
          <>
            {isError && (
              <Alert variant="warning" className="mb-3" title="Não foi possível atualizar o histórico.">
                <p>Mostrando os últimos dados recebidos. {error.message}</p>
                <Button size="sm" className="mt-2" loading={isFetching} onClick={() => void refetch()}>
                  Tentar novamente
                </Button>
              </Alert>
            )}
            <div
              aria-busy={isPlaceholderData || undefined}
              className={cn(
                "grid gap-4 xl:grid-cols-3 motion-safe:transition-opacity",
                isPlaceholderData && "opacity-60",
              )}
            >
              <Panel title="Uso de CPU" headingLevel={3}>
                {body(
                  <LineChart
                    series={series.cpu}
                    stepSeconds={step}
                    format={formatPercent}
                    yMax={100}
                    domain={domain}
                  />,
                )}
              </Panel>
              <Panel title="Memória usada" headingLevel={3}>
                {body(
                  <LineChart
                    series={series.mem}
                    stepSeconds={step}
                    format={formatBytes}
                    yMax={series.memTotal}
                    domain={domain}
                  />,
                )}
              </Panel>
              <Panel title="Tráfego de rede" headingLevel={3}>
                {body(<LineChart series={series.net} stepSeconds={step} format={formatRate} domain={domain} />)}
              </Panel>
            </div>
          </>
        )}
      </div>
    </section>
  );
}

// --- PM2 ---------------------------------------------------------------------

const pm2Status: Record<string, BadgeVariant> = {
  online: "success",
  stopped: "neutral",
  errored: "danger",
  launching: "warning",
  stopping: "warning",
};

function PM2Panel() {
  const { data } = usePM2();
  if (!data?.available) return null;
  return (
    <Panel
      title="Processos PM2"
      description="Atualizado a cada 10 s."
      actions={
        data.daemonRunning && (
          <Badge size="sm" variant="neutral">
            {data.processes.length} {data.processes.length === 1 ? "processo" : "processos"}
          </Badge>
        )
      }
    >
      {!data.daemonRunning ? (
        <Alert variant="warning" title="O PM2 está instalado, mas o daemon não está ativo." />
      ) : data.processes.length === 0 ? (
        <PanelState>Nenhum processo registrado no PM2.</PanelState>
      ) : (
        <div className="-m-4 overflow-x-auto">
          <table className="w-full text-left text-[13px]">
            <thead>
              <tr className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">
                <th scope="col" className="px-4 py-2 font-normal">Nome</th>
                <th scope="col" className="px-4 py-2 font-normal">Status</th>
                <th scope="col" className="px-4 py-2 text-right font-normal">CPU</th>
                <th scope="col" className="px-4 py-2 text-right font-normal">RAM</th>
                <th scope="col" className="px-4 py-2 text-right font-normal">Reinícios</th>
                <th scope="col" className="px-4 py-2 font-normal">Diretório</th>
              </tr>
            </thead>
            <tbody>
              {data.processes.map((p) => (
                <tr key={p.pmId} className="border-t border-line hover:bg-surface-2">
                  <td className="max-w-48 truncate px-4 py-2 font-medium text-fg" translate="no" title={p.name}>
                    {p.name}
                  </td>
                  <td className="px-4 py-2">
                    <Badge size="sm" variant={pm2Status[p.status] ?? "warning"} dot>
                      {p.status}
                    </Badge>
                  </td>
                  <td className="px-4 py-2 text-right font-mono tabular-nums">{formatPercent(p.cpuPercent)}</td>
                  <td className="px-4 py-2 text-right font-mono tabular-nums">{formatBytes(p.memoryBytes)}</td>
                  <td className="px-4 py-2 text-right font-mono tabular-nums">{p.restarts}</td>
                  <td
                    className="max-w-72 truncate px-4 py-2 font-mono text-xs text-fg-muted"
                    translate="no"
                    title={p.cwd}
                  >
                    {p.cwd}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

// --- Página ------------------------------------------------------------------

/** Lembrete enquanto o painel não tem domínio (só HTTP pela porta 8000). */
function PanelDomainReminder() {
  const { data } = usePanelSettings();
  if (!data || data.domain) return null;
  return (
    <Alert variant="info" title="Configure o domínio do painel para usar HTTPS">
      <p>
        Hoje o painel responde pela porta 8000, sem criptografia.{" "}
        <Link href="/configuracoes/painel/" className={cn("text-accent-text hover:text-fg hover:underline", focusRing)}>
          Configurar domínio <span aria-hidden="true">→</span>
        </Link>
      </p>
    </Alert>
  );
}

export default function VisaoGeralPage() {
  const current = useMetricsCurrent();
  const live = useLiveMetrics(current.data);
  const sample = live?.sample ?? current.data;
  const nowId = useId();

  return (
    <>
      <PageHeader eyebrow="Painel" title="Visão geral" description="Informações do servidor onde o KoreGO está rodando." />

      <div className="space-y-8">
        <div className="space-y-3 empty:hidden">
          <ProxyBanner />
          <PanelDomainReminder />
        </div>
        <ServerSection />

        <section aria-labelledby={nowId}>
          <SectionHeading id={nowId} title="Agora">
            <Badge size="sm" variant={live ? "success" : "neutral"} dot>
              {live ? "Ao vivo · 2 s" : "Instantâneo"}
            </Badge>
          </SectionHeading>
          {current.isError && !live && (
            <Alert variant="danger" className="mb-3" title="Não foi possível carregar as métricas.">
              <p>{current.error.message}</p>
            </Alert>
          )}
          <LiveSection sample={sample} hist={live?.hist ?? null} />
          <div className="mt-4 grid gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
            <DiskPanel sample={sample} pending={current.isPending} />
            <ContainersPanel />
          </div>
        </section>

        <HistorySection memTotal={sample?.memTotalBytes} />

        <PM2Panel />
      </div>
    </>
  );
}
