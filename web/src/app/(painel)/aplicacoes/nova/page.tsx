"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { AppForm } from "@/components/apps/app-form";
import { Breadcrumb, Loading, NotFound, PROJECTS_HREF, appHref, parseId, projectHref } from "@/components/apps/page-bits";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Button, Panel } from "@/components/ui";
import { useProjects } from "@/lib/projects";

function FromQuery() {
  const router = useRouter();
  const id = parseId(useSearchParams().get("projeto"));
  const projects = useProjects();
  const [created, setCreated] = useState(false);

  const notFound = (
    <NotFound title="Projeto não encontrado">Escolha um projeto antes de criar a aplicação.</NotFound>
  );
  if (id === null) return notFound;
  if (projects.isPending) return <Loading label="Carregando o projeto…" />;
  if (projects.isError) {
    return (
      <>
        <h1 className="sr-only">Nova aplicação</h1>
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

  if (created) {
    return (
      <Panel>
        <PanelState>
          <span role="status">Aplicação criada. Abrindo…</span>
        </PanelState>
      </Panel>
    );
  }

  return (
    <>
      <Breadcrumb
        trail={[
          { label: "Projetos", href: PROJECTS_HREF },
          { label: project.name, href: projectHref(project.id) },
          { label: "Nova aplicação" },
        ]}
      />
      <PageHeader
        eyebrow={project.name}
        title="Nova aplicação"
        description="De onde vem o código, como ele é construído e em que porta responde."
      />
      <div className="max-w-3xl">
        <AppForm
          projectId={project.id}
          onSaved={(a) => {
            setCreated(true);
            router.push(appHref(a.id, "visao"));
          }}
        />
      </div>
    </>
  );
}

export default function NewAppPage() {
  return (
    <Suspense fallback={<Loading label="Carregando…" />}>
      <FromQuery />
    </Suspense>
  );
}
