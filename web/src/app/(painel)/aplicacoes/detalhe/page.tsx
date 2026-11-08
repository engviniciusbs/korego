"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useId, useState, type FormEvent, type ReactNode } from "react";
import { AppForm, COMPOSE_WARNING } from "@/components/apps/app-form";
import { EnvEditor } from "@/components/apps/env-editor";
import { DeployList } from "@/components/deploys/deploy-list";
import { linkCls } from "@/components/apps/form-bits";
import {
  AppStatus,
  Breadcrumb,
  DomainLinks,
  Loading,
  NotFound,
  PROJECTS_HREF,
  deployErrorMessage,
  parseId,
  projectHref,
  shortSha,
} from "@/components/apps/page-bits";
import { PageHeader } from "@/components/shell/page-header";
import { Alert, Button, Dialog, Input, Label, Panel, Tabs, tabId, tabPanelId } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { useApp, useDeleteApp, type AppView } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { isActive, statusLabel, useDeploys, useStartDeploy } from "@/lib/deploys";
import { formatDateTime } from "@/lib/format";
import { useProjects } from "@/lib/projects";

type Tab = "visao" | "deploys" | "variaveis" | "configuracoes";
const TABS: { value: Tab; label: string }[] = [
  { value: "visao", label: "Visão" },
  { value: "deploys", label: "Deploys" },
  { value: "variaveis", label: "Variáveis" },
  { value: "configuracoes", label: "Configurações" },
];
const isTab = (v: string | null): v is Tab => TABS.some((t) => t.value === v);

const BUILD_LABEL: Record<string, string> = {
  dockerfile: "Dockerfile",
  compose: "Docker Compose",
  railpack: "Railpack",
  image: "Imagem pronta",
};
const SOURCE_LABEL: Record<string, string> = { github: "GitHub", git: "URL Git", image: "Imagem" };

function Fact({ label, children, mono = true }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className="min-w-0 bg-surface px-4 py-3">
      <dt className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">{label}</dt>
      <dd className={cn("mt-1 text-[13px] text-fg [overflow-wrap:anywhere]", mono && "font-mono")}>{children}</dd>
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

// --- Visão --------------------------------------------------------------------

/** Último deploy, quando ele não é o atual (em andamento ou falhou). */
function LatestDeploy({ app, onOpen }: { app: AppView; onOpen: () => void }) {
  const deploys = useDeploys(app.id, 1);
  const last = deploys.data?.[0];
  if (!last || last.id === app.currentDeploy?.id) return null;
  const more = (
    <button type="button" onClick={onOpen} className={cn("ml-1", linkCls)}>
      Ver em Deploys
    </button>
  );
  if (isActive(last.status)) {
    return (
      <Alert variant="info" title={`Deploy #${last.id} em andamento: ${statusLabel(last.status)}.`}>
        <p>A versão atual continua no ar até o novo deploy ficar saudável.{more}</p>
      </Alert>
    );
  }
  if (last.status === "failed") {
    return (
      <Alert variant="danger" title={`O deploy #${last.id} falhou.`}>
        <p>
          {last.reason || "Sem detalhes."}
          {app.currentDeploy ? " A versão anterior continua no ar." : ""}
          {more}
        </p>
      </Alert>
    );
  }
  return null;
}

function Overview({ app, onDeployed, onOpenDeploys }: { app: AppView; onDeployed: () => void; onOpenDeploys: () => void }) {
  const deploy = useStartDeploy();
  const d = app.currentDeploy;
  const finished = d?.finishedAt ? formatDateTime(d.finishedAt) : null;
  return (
    <div className="space-y-4">
      <LatestDeploy app={app} onOpen={onOpenDeploys} />
      {app.buildType === "compose" && <Alert variant="info">{COMPOSE_WARNING}</Alert>}

      <Panel
        title="Resumo"
        variant="accent"
        actions={
          <Button
            variant="primary"
            size="sm"
            loading={deploy.isPending}
            onClick={() => deploy.mutate(app.id, { onSuccess: onDeployed })}
          >
            Deploy agora
          </Button>
        }
      >
        {deploy.isError && (
          <Alert variant="danger" className="mb-4">
            {deployErrorMessage(deploy.error)}
          </Alert>
        )}
        <dl className="-m-4 grid grid-cols-1 gap-px bg-line sm:grid-cols-2 xl:grid-cols-4">
          <Fact label="Status" mono={false}>
            <AppStatus app={app} />
          </Fact>
          <Fact label="Deploy atual">
            {d ? (
              <span className="flex flex-wrap items-baseline gap-x-2">
                {d.commitSha ? (
                  <span title={d.commitSha} translate="no">
                    {shortSha(d.commitSha)}
                  </span>
                ) : (
                  <span>#{d.id}</span>
                )}
                {finished && <span className="text-[11px] text-fg-subtle">{finished}</span>}
              </span>
            ) : (
              <Dash label="nenhum deploy ainda" />
            )}
          </Fact>
          <Fact label="Endereço local">
            {app.url ? (
              <span translate="no">{app.url}</span>
            ) : (
              <span className="font-sans text-fg-subtle">publicado após o primeiro deploy</span>
            )}
          </Fact>
          <Fact label="Origem">
            <span translate="no">
              {SOURCE_LABEL[app.sourceType] ?? app.sourceType} · {app.sourceType === "image" ? app.image : app.repo}
              {app.branch && app.sourceType !== "image" ? ` @ ${app.branch}` : ""}
            </span>
          </Fact>
          <Fact label="Build">{BUILD_LABEL[app.buildType] ?? app.buildType}</Fact>
          <Fact label="Porta interna">
            <span className="tabular-nums">{app.internalPort}</span>
          </Fact>
          <Fact label="Verificação de saúde">
            <span className="tabular-nums">{app.healthTimeoutS} s</span>
          </Fact>
          <Fact label="Deploy automático" mono={false}>
            {app.sourceType === "github" ? (app.autoDeploy ? "a cada push" : "desligado") : "só com GitHub"}
          </Fact>
        </dl>
      </Panel>

      <Panel
        title="Domínios"
        size="sm"
        actions={
          <Link href="/dominios/" className={cn("text-xs", linkCls)}>
            Gerenciar em Domínios <span aria-hidden="true">→</span>
          </Link>
        }
      >
        <DomainLinks
          domains={app.domains}
          empty={
            <p className="text-xs text-fg-subtle">
              Nenhum domínio aponta para esta aplicação. Adicione um em Domínios para publicá-la com HTTPS.
            </p>
          }
        />
      </Panel>
    </div>
  );
}

// --- Configurações -------------------------------------------------------------

function DangerZone({ app }: { app: AppView }) {
  const uid = useId();
  const router = useRouter();
  const remove = useDeleteApp();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [removed, setRemoved] = useState(false);
  const formId = `${uid}-form`;
  const matches = typed.trim() === app.slug;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!matches || remove.isPending) return;
    remove.mutate(app.id, {
      onSuccess: () => {
        setRemoved(true);
        router.push(projectHref(app.projectId));
      },
    });
  };

  const err = remove.error;
  const errorBody: ReactNode =
    err instanceof ApiError && err.code === "APP_HAS_DOMAINS" ? (
      <>
        <p>
          A aplicação ainda tem domínios apontando para ela. Remova-os, ou aponte-os para outro destino, em{" "}
          <Link href="/dominios/" className={linkCls}>
            Domínios
          </Link>{" "}
          e tente de novo.
        </p>
        {app.domains.length > 0 && (
          <p className="mt-1 font-mono text-xs" translate="no">
            {app.domains.join(", ")}
          </p>
        )}
      </>
    ) : err instanceof ApiError && err.code === "APP_DELETING" ? (
      "A aplicação está sendo removida."
    ) : (
      err?.message
    );

  return (
    <Panel title="Zona de perigo" variant="danger" size="sm">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-prose text-[13px] text-fg-muted">
          Remove os containers, as imagens e as variáveis da aplicação. Domínios precisam ser removidos antes.
        </p>
        <Button
          variant="danger"
          onClick={() => {
            remove.reset();
            setTyped("");
            setOpen(true);
          }}
        >
          Remover aplicação
        </Button>
      </div>
      <Dialog
        open={open}
        variant="danger"
        eyebrow="Ação irreversível"
        title="Remover aplicação?"
        confirmLabel="Remover"
        formId={formId}
        loading={remove.isPending || removed}
        confirmDisabled={!matches}
        onCancel={() => setOpen(false)}
      >
        <form id={formId} noValidate onSubmit={submit} className="space-y-3">
          <p>
            <span className="font-mono text-fg" translate="no">
              {app.slug}
            </span>{" "}
            sai do ar e é apagada com o histórico de deploys e as variáveis.
          </p>
          <div>
            <Label htmlFor={`${uid}-confirm`} className="normal-case tracking-normal">
              Digite <span className="mx-1 font-mono text-fg" translate="no">{app.slug}</span> para confirmar
            </Label>
            <Input
              id={`${uid}-confirm`}
              name="confirm"
              mono
              className="mt-1.5"
              value={typed}
              autoComplete="off"
              spellCheck={false}
              autoCapitalize="none"
              onChange={(e) => setTyped(e.target.value)}
            />
          </div>
          {remove.isError && <Alert variant="danger">{errorBody}</Alert>}
        </form>
      </Dialog>
    </Panel>
  );
}

// --- Página --------------------------------------------------------------------

function AppDetail({ id, initialTab }: { id: number; initialTab: Tab }) {
  const app = useApp(id);
  const projects = useProjects();
  const tabsId = useId();
  const [tab, setTab] = useState<Tab>(initialTab);
  // As variáveis ficam montadas depois da primeira visita: trocar de aba não perde alterações não salvas.
  const [envOpened, setEnvOpened] = useState(initialTab === "variaveis");

  const changeTab = (t: Tab) => {
    setTab(t);
    if (t === "variaveis") setEnvOpened(true);
    // A aba vai para a URL (recarregar ou compartilhar mantém a seção) sem nova navegação.
    window.history.replaceState(null, "", `?id=${id}&aba=${t}`);
  };

  if (app.isPending) return <Loading label="Carregando a aplicação…" />;
  if (app.isError) {
    const e = app.error;
    if (e instanceof ApiError && (e.code === "APP_NOT_FOUND" || e.code === "INVALID_APP_ID" || e.status === 404)) {
      return <NotFound title="Aplicação não encontrada">Ela pode ter sido removida, ou o endereço está incompleto.</NotFound>;
    }
    return (
      <>
        <Breadcrumb trail={[{ label: "Projetos", href: PROJECTS_HREF }]} />
        <h1 className="sr-only">Aplicação</h1>
        <Alert variant="danger" title="Não foi possível carregar a aplicação.">
          <p>{e.message}</p>
          <Button size="sm" className="mt-2" loading={app.isFetching} onClick={() => void app.refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      </>
    );
  }

  const a = app.data;
  const project = projects.data?.find((p) => p.id === a.projectId);

  return (
    <>
      <Breadcrumb
        trail={[
          { label: "Projetos", href: PROJECTS_HREF },
          { label: project?.name ?? `Projeto ${a.projectId}`, href: projectHref(a.projectId) },
          { label: a.slug },
        ]}
      />
      <PageHeader
        title={a.slug}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <AppStatus app={a} />
            <span className="font-mono text-[11px] text-fg-subtle" translate="no">
              {a.sourceType === "image" ? a.image : a.repo}
            </span>
          </span>
        }
      />

      <div className="mb-4 overflow-x-auto">
        <Tabs id={tabsId} label="Seções da aplicação" items={TABS} value={tab} onChange={changeTab} />
      </div>

      <div role="tabpanel" id={tabPanelId(tabsId)} aria-labelledby={tabId(tabsId, tab)}>
        {tab === "visao" && <Overview app={a} onDeployed={() => changeTab("deploys")} onOpenDeploys={() => changeTab("deploys")} />}
        {tab === "deploys" && <DeployList app={a} />}
        {envOpened && (
          <div hidden={tab !== "variaveis"}>
            <EnvEditor appId={a.id} />
          </div>
        )}
        {tab === "configuracoes" && (
          <div className="max-w-3xl space-y-4">
            <AppForm key={a.id} projectId={a.projectId} app={a} onSaved={() => undefined} />
            <DangerZone app={a} />
          </div>
        )}
      </div>
    </>
  );
}

function FromQuery() {
  const params = useSearchParams();
  const id = parseId(params.get("id"));
  const aba = params.get("aba");
  if (id === null) {
    return <NotFound title="Aplicação não encontrada">O endereço está incompleto.</NotFound>;
  }
  return <AppDetail key={id} id={id} initialTab={isTab(aba) ? aba : "visao"} />;
}

export default function AppDetailPage() {
  return (
    <Suspense fallback={<Loading label="Carregando a aplicação…" />}>
      <FromQuery />
    </Suspense>
  );
}
