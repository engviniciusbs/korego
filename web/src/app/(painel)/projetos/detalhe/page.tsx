"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { linkCls } from "@/components/apps/form-bits";
import {
  AppStatus,
  Breadcrumb,
  DomainLinks,
  Loading,
  NotFound,
  PROJECTS_HREF,
  appHref,
  buttonLinkCls,
  deployErrorMessage,
  parseId,
  shortSha,
  sourceSummary,
} from "@/components/apps/page-bits";
import { ProjectDialog } from "@/components/apps/project-dialog";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Button, Panel, Spinner } from "@/components/ui";
import { useApps, type AppView } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { useStartDeploy } from "@/lib/deploys";
import { useProjects, type Project } from "@/lib/projects";

const newAppHref = (id: number) => `/aplicacoes/nova/?projeto=${id}`;

function AppRow({ app }: { app: AppView }) {
  const router = useRouter();
  const deploy = useStartDeploy();
  return (
    <li className="grid gap-x-4 gap-y-2 border-t border-line px-4 py-3 first:border-t-0 md:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_auto] md:items-center">
      <div className="min-w-0 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <Link
            href={appHref(app.id, "visao")}
            translate="no"
            className={cn("font-mono text-[13px] font-medium text-fg hover:text-accent-text", linkCls)}
          >
            {app.slug}
          </Link>
          <AppStatus app={app} />
          {app.currentDeploy?.commitSha && (
            <span className="font-mono text-[11px] text-fg-subtle" translate="no">
              <span className="sr-only">commit </span>
              {shortSha(app.currentDeploy.commitSha)}
            </span>
          )}
        </div>
        <p className="truncate font-mono text-[11px] text-fg-subtle" translate="no" title={sourceSummary(app)}>
          {sourceSummary(app)}
        </p>
      </div>
      <div className="min-w-0">
        <span className="sr-only">Domínios: </span>
        <DomainLinks domains={app.domains} empty={<span className="text-xs text-fg-subtle">Sem domínio</span>} />
      </div>
      <div className="flex flex-col items-start gap-1 md:items-end">
        <Button
          size="sm"
          loading={deploy.isPending}
          onClick={() =>
            deploy.mutate(app.id, { onSuccess: () => router.push(appHref(app.id, "deploys")) })
          }
        >
          Deploy<span className="sr-only"> de {app.slug}</span>
        </Button>
        {deploy.isError && (
          <p role="alert" className="max-w-64 text-xs text-danger md:text-right">
            {deployErrorMessage(deploy.error)}
          </p>
        )}
      </div>
    </li>
  );
}

function ProjectApps({ project }: { project: Project }) {
  const apps = useApps(project.id);
  const [editing, setEditing] = useState(false);
  const list = [...(apps.data ?? [])].sort((a, b) => a.slug.localeCompare(b.slug));

  return (
    <>
      <Breadcrumb trail={[{ label: "Projetos", href: PROJECTS_HREF }, { label: project.name }]} />
      <PageHeader
        title={project.name}
        description={project.description || undefined}
        actions={
          <>
            <Button onClick={() => setEditing(true)}>Editar projeto</Button>
            <Link href={newAppHref(project.id)} className={buttonLinkCls(true)}>
              Nova aplicação
            </Link>
          </>
        }
      />

      <Panel
        title="Aplicações"
        description={apps.isSuccess ? `${list.length} neste projeto` : undefined}
        size="sm"
        className="[&>div]:p-0"
      >
        {apps.isPending ? (
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">Carregando aplicações…</span>
          </PanelState>
        ) : apps.isError ? (
          <div className="p-3">
            <Alert variant="danger" title="Não foi possível listar as aplicações.">
              <p>{apps.error.message}</p>
              <Button size="sm" className="mt-2" onClick={() => void apps.refetch()}>
                Tentar novamente
              </Button>
            </Alert>
          </div>
        ) : list.length === 0 ? (
          <PanelState>
            <span>Nenhuma aplicação neste projeto.</span>
            <Link href={newAppHref(project.id)} className={linkCls}>
              Criar a primeira
            </Link>
          </PanelState>
        ) : (
          <ul aria-label={`Aplicações de ${project.name}`}>
            {list.map((a) => (
              <AppRow key={a.id} app={a} />
            ))}
          </ul>
        )}
      </Panel>

      {editing && <ProjectDialog project={project} onClose={() => setEditing(false)} />}
    </>
  );
}

function FromQuery() {
  const id = parseId(useSearchParams().get("id"));
  const projects = useProjects();
  const notFound = <NotFound title="Projeto não encontrado">Ele pode ter sido removido, ou o endereço está incompleto.</NotFound>;
  if (id === null) return notFound;
  if (projects.isPending) return <Loading label="Carregando o projeto…" />;
  if (projects.isError) {
    return (
      <>
        <Breadcrumb trail={[{ label: "Projetos", href: PROJECTS_HREF }]} />
        <h1 className="sr-only">Projeto</h1>
        <Alert variant="danger" title="Não foi possível carregar o projeto.">
        <p>{projects.error.message}</p>
        <Button size="sm" className="mt-2" loading={projects.isFetching} onClick={() => void projects.refetch()}>
          Tentar novamente
        </Button>
        </Alert>
      </>
    );
  }
  const project = projects.data.find((p) => p.id === id);
  if (!project) return notFound;
  return <ProjectApps key={id} project={project} />;
}

export default function ProjectDetailPage() {
  return (
    <Suspense fallback={<Loading label="Carregando o projeto…" />}>
      <FromQuery />
    </Suspense>
  );
}
