"use client";

import type { ReactNode } from "react";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Panel, Spinner } from "@/components/ui";
import { formatGiB, formatUptime } from "@/lib/format";
import { useSystemInfo } from "@/lib/system";
import { useLive } from "@/lib/ws";

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

export default function VisaoGeralPage() {
  const { data, isPending, isError, error, refetch, isFetching } = useSystemInfo();
  const live = useLive<{ uptimeSeconds: number }>("system");
  const uptime = live?.uptimeSeconds ?? data?.uptimeSeconds;

  return (
    <>
      <PageHeader eyebrow="Painel" title="Visão geral" description="Informações do servidor onde o KoreGO está rodando." />

      {isPending ? (
        <Panel title="Servidor">
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">Carregando informações do servidor…</span>
          </PanelState>
        </Panel>
      ) : isError ? (
        <Alert variant="danger" title="Não foi possível carregar as informações do servidor.">
          <p>{error.message}</p>
          <Button size="sm" className="mt-2" loading={isFetching} onClick={() => void refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      ) : (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
          <Panel title="Servidor" variant="accent">
            <dl className="-m-4 grid grid-cols-1 gap-px bg-line sm:grid-cols-2 xl:grid-cols-3">
              <Fact label="Hostname">{data.hostname}</Fact>
              <Fact label="Sistema">
                {[data.platform || data.os, data.platformVersion].filter(Boolean).join(" ")}
              </Fact>
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
      )}
    </>
  );
}
