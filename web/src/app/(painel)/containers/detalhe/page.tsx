"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useId, useState, type ReactNode } from "react";
import { ContainerActions } from "@/components/containers/container-actions";
import { formatPort, healthVariant, stateVariant } from "@/components/containers/container-table";
import { LogViewer } from "@/components/containers/log-viewer";
import { StatsPanel } from "@/components/containers/stats-panel";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Panel, Spinner, Tabs, tabId, tabPanelId } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { useContainer, useContainersLive, type ContainerDetails } from "@/lib/containers";
import { formatDateTime } from "@/lib/format";

const ID_RE = /^[a-f0-9]{12,64}$/;
const LIST_HREF = "/containers/";

type Tab = "detalhes" | "logs" | "stats";
const isTab = (v: string | null): v is Tab => v === "detalhes" || v === "logs" || v === "stats";
const TABS: { value: Tab; label: string }[] = [
  { value: "detalhes", label: "Detalhes" },
  { value: "logs", label: "Logs" },
  { value: "stats", label: "Stats" },
];

const linkClass = cn("text-accent-text underline-offset-2 hover:underline", focusRing);

function Breadcrumb({ name }: { name?: string }) {
  return (
    <nav aria-label="Trilha" className="mb-3 font-mono text-[11px] uppercase tracking-[0.12em] text-fg-subtle">
      <Link href={LIST_HREF} className={linkClass}>
        Containers
      </Link>
      {name && (
        <>
          <span aria-hidden="true" className="mx-2">
            /
          </span>
          <span aria-current="page" translate="no" className="normal-case tracking-normal [overflow-wrap:anywhere]">
            {name}
          </span>
        </>
      )}
    </nav>
  );
}

function NotFound() {
  return (
    <>
      <Breadcrumb />
      <Panel variant="danger">
        <div className="space-y-2 py-4 text-center">
          <h1 className="text-base font-semibold text-fg">Container não encontrado</h1>
          <p className="text-[13px] text-fg-muted">
            Ele pode ter sido removido, ou o endereço está incompleto.
          </p>
          <p className="text-[13px]">
            <Link href={LIST_HREF} className={linkClass}>
              Voltar para a lista de containers
            </Link>
          </p>
        </div>
      </Panel>
    </>
  );
}

function Loading() {
  return (
    <Panel>
      <PanelState>
        <Spinner size="sm" decorative />
        <span role="status">Consultando o Docker…</span>
      </PanelState>
    </Panel>
  );
}

// --- Detalhes ----------------------------------------------------------------

function Fact({ label, children, mono = true }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className="min-w-0 bg-surface px-4 py-3">
      <dt className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">{label}</dt>
      <dd className={cn("mt-1 text-[13px] text-fg [overflow-wrap:anywhere]", mono && "font-mono")} translate="no">
        {children}
      </dd>
    </div>
  );
}

const Dash = ({ label }: { label: string }) => (
  <>
    <span aria-hidden="true" className="text-fg-subtle">
      —
    </span>
    <span className="sr-only">{label}</span>
  </>
);

/** Docker usa o ano 1 para "nunca iniciado". */
function when(iso: string): string | null {
  const d = new Date(iso);
  if (!iso || Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return null;
  return formatDateTime(iso);
}

const th = "px-4 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const td = "px-4 py-2 align-top font-mono text-xs";

function ListPanel({ title, items, empty }: { title: string; items: string[]; empty: string }) {
  return (
    <Panel title={title} size="sm">
      {items.length === 0 ? (
        <p className="text-xs text-fg-subtle">{empty}</p>
      ) : (
        <ul className="space-y-1 font-mono text-xs tabular-nums text-fg" translate="no">
          {items.map((i) => (
            <li key={i} className="[overflow-wrap:anywhere]">
              {i}
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function DetailsTab({ c }: { c: ContainerDetails }) {
  const ports = [...new Set(c.ports.map(formatPort))];
  const created = when(c.createdAt);
  const started = when(c.startedAt);
  return (
    <div className="space-y-4">
      <Panel title="Resumo" variant="accent">
        <dl className="-m-4 grid grid-cols-1 gap-px bg-line sm:grid-cols-2 xl:grid-cols-4">
          <Fact label="Imagem">{c.image}</Fact>
          <Fact label="Estado">
            <Badge size="sm" dot variant={stateVariant[c.state] ?? "neutral"}>
              {c.state}
            </Badge>
          </Fact>
          <Fact label="Health">
            {c.health ? (
              <Badge size="sm" variant={healthVariant[c.health] ?? "neutral"}>
                {c.health}
              </Badge>
            ) : (
              <span className="text-fg-subtle">sem health check</span>
            )}
          </Fact>
          <Fact label="Reinícios">
            <span className="tabular-nums">{c.restartCount}</span>
          </Fact>
          <Fact label="Criado em">{created ?? <Dash label="desconhecido" />}</Fact>
          <Fact label="Iniciado em">{started ?? <Dash label="nunca iniciado" />}</Fact>
          <Fact label="Projeto">
            {c.project ? `${c.project}${c.service ? ` · ${c.service}` : ""}` : <span className="text-fg-subtle">avulso</span>}
          </Fact>
          <Fact label="ID">
            <span title={c.id}>{c.id.slice(0, 12)}</span>
          </Fact>
        </dl>
      </Panel>

      <Panel title="Comando" size="sm">
        {c.command ? (
          <code className="block whitespace-pre-wrap font-mono text-xs text-fg [overflow-wrap:anywhere]" translate="no">
            {c.command}
          </code>
        ) : (
          <p className="text-xs text-fg-subtle">Sem comando definido.</p>
        )}
      </Panel>

      <div className="grid gap-4 md:grid-cols-2">
        <ListPanel title="Portas" items={ports} empty="Nenhuma porta publicada." />
        <ListPanel title="Redes" items={c.networks} empty="Sem redes." />
      </div>

      <Panel title="Volumes" size="sm" className="[&>div]:p-0">
        {c.mounts.length === 0 ? (
          <p className="p-3 text-xs text-fg-subtle">Nenhum volume montado.</p>
        ) : (
          <div className="relative overflow-x-auto">
            <table className="w-full min-w-[36rem]">
              <caption className="sr-only">Volumes montados</caption>
              <thead>
                <tr>
                  <th scope="col" className={th}>Tipo</th>
                  <th scope="col" className={th}>Origem</th>
                  <th scope="col" className={th}>Destino</th>
                  <th scope="col" className={th}>Acesso</th>
                </tr>
              </thead>
              <tbody translate="no">
                {c.mounts.map((m) => (
                  <tr key={`${m.source}→${m.destination}`} className="border-t border-line">
                    <td className={cn(td, "text-fg-muted")}>{m.type}</td>
                    <td className={cn(td, "text-fg [overflow-wrap:anywhere]")}>{m.source}</td>
                    <td className={cn(td, "text-fg [overflow-wrap:anywhere]")}>{m.destination}</td>
                    <td className={cn(td, "whitespace-nowrap text-fg-muted")}>
                      {m.readWrite ? "leitura e escrita" : "somente leitura"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      <Panel
        title="Variáveis de ambiente"
        description="Só os nomes: os valores nunca saem do servidor."
        size="sm"
        className="[&>div]:p-0"
      >
        {c.envNames.length === 0 ? (
          <p className="p-3 text-xs text-fg-subtle">Nenhuma variável definida.</p>
        ) : (
          <table className="w-full">
            <caption className="sr-only">Variáveis de ambiente</caption>
            <thead>
              <tr>
                <th scope="col" className={th}>Nome</th>
                <th scope="col" className={th}>Valor</th>
              </tr>
            </thead>
            <tbody translate="no">
              {c.envNames.map((n) => (
                <tr key={n} className="border-t border-line">
                  <td className={cn(td, "text-fg [overflow-wrap:anywhere]")}>{n}</td>
                  <td className={cn(td, "text-fg-subtle")}>
                    <span aria-hidden="true">••••••</span>
                    <span className="sr-only">valor oculto</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  );
}

// --- Página ------------------------------------------------------------------

function ContainerDetail({ id, initialTab }: { id: string; initialTab: Tab }) {
  const { data: c, isPending, isError, error, refetch, isFetching } = useContainer(id);
  useContainersLive();
  const router = useRouter();
  const tabsId = useId();
  const [tab, setTab] = useState<Tab>(initialTab);
  // Os logs ficam montados depois da primeira visita: trocar de aba não perde o buffer.
  const [logsOpened, setLogsOpened] = useState(initialTab === "logs");
  const [removed, setRemoved] = useState(false);

  const changeTab = (t: Tab) => {
    setTab(t);
    if (t === "logs") setLogsOpened(true);
    // A aba vai para a URL (recarregar ou compartilhar mantém a seção) sem nova navegação.
    window.history.replaceState(null, "", `?id=${id}${t === "detalhes" ? "" : `&aba=${t}`}`);
  };

  if (removed) {
    return (
      <Panel>
        <PanelState>
          <span role="status">Container removido. Voltando para a lista…</span>
        </PanelState>
      </Panel>
    );
  }
  if (isPending) return <Loading />;
  if (isError) {
    const code = error instanceof ApiError ? error.code : "";
    const status = error instanceof ApiError ? error.status : 0;
    if (code === "CONTAINER_NOT_FOUND" || code === "INVALID_CONTAINER_ID" || status === 404) return <NotFound />;
    const unavailable = code === "DOCKER_UNAVAILABLE" || status === 503;
    return (
      <>
        <Breadcrumb />
        <h1 className="sr-only">Container</h1>
        <Alert
          variant={unavailable ? "warning" : "danger"}
          title={unavailable ? "Docker indisponível" : "Não foi possível carregar o container."}
        >
          <p>
            {unavailable
              ? "O daemon do Docker não respondeu. Verifique se ele está em execução no servidor."
              : error.message}
          </p>
          <Button size="sm" className="mt-2" loading={isFetching} onClick={() => void refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      </>
    );
  }

  return (
    <>
      <Breadcrumb name={c.name} />
      <PageHeader
        title={c.name}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <Badge size="sm" variant={c.managed ? "accent" : "neutral"}>
              {c.managed ? "gerenciado" : "externo"}
            </Badge>
            <Badge size="sm" dot variant={stateVariant[c.state] ?? "neutral"}>
              {c.state}
            </Badge>
            <span className="font-mono text-[11px] text-fg-subtle" translate="no">
              {c.image}
            </span>
          </span>
        }
        actions={
          <ContainerActions
            container={c}
            onDone={(action) => {
              if (action !== "remove") return;
              setRemoved(true);
              router.push(LIST_HREF);
            }}
          />
        }
      />

      <Tabs id={tabsId} label="Seções do container" items={TABS} value={tab} onChange={changeTab} className="mb-4" />

      <div role="tabpanel" id={tabPanelId(tabsId)} aria-labelledby={tabId(tabsId, tab)}>
        {tab === "detalhes" && <DetailsTab c={c} />}
        {logsOpened && (
          <div hidden={tab !== "logs"}>
            <LogViewer containerId={c.id} containerName={c.name} active={tab === "logs"} />
          </div>
        )}
        {tab === "stats" && <StatsPanel containerId={c.id} />}
      </div>
    </>
  );
}

function FromQuery() {
  const params = useSearchParams();
  const id = params.get("id") ?? "";
  const aba = params.get("aba");
  if (!ID_RE.test(id)) return <NotFound />;
  return <ContainerDetail key={id} id={id} initialTab={isTab(aba) ? aba : "detalhes"} />;
}

export default function ContainerDetailPage() {
  return (
    <Suspense fallback={<Loading />}>
      <FromQuery />
    </Suspense>
  );
}
