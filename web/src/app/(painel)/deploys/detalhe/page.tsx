"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense } from "react";
import { linkCls } from "@/components/apps/form-bits";
import { Breadcrumb, Loading, PROJECTS_HREF, appHref, parseId } from "@/components/apps/page-bits";
import { DeployLog } from "@/components/deploys/deploy-log";
import { Alert, Button, Panel } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { useApp } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { useDeploy } from "@/lib/deploys";

function DeployNotFound() {
  return (
    <>
      <Breadcrumb trail={[{ label: "Projetos", href: PROJECTS_HREF }]} />
      <Panel variant="danger">
        <div className="space-y-2 py-4 text-center">
          <h1 className="text-base font-semibold text-fg">Deploy não encontrado</h1>
          <p className="text-[13px] text-fg-muted">Ele pode ter sido removido com a aplicação, ou o endereço está incompleto.</p>
          <p className="text-[13px]">
            <Link href={PROJECTS_HREF} className={linkCls}>
              Voltar para os projetos
            </Link>
          </p>
        </div>
      </Panel>
    </>
  );
}

function AppCrumbs({ appId, deployId }: { appId: number; deployId: number }) {
  const app = useApp(appId);
  const back = appHref(appId, "deploys");
  return (
    <>
      <Breadcrumb
        trail={[
          { label: "Projetos", href: PROJECTS_HREF },
          { label: app.data?.slug ?? `Aplicação ${appId}`, href: back },
          { label: `Deploy #${deployId}` },
        ]}
      />
      <div className="mb-4 flex flex-wrap items-baseline justify-between gap-3 border-b border-line pb-3">
        <h1 className="text-xl font-semibold tracking-tight text-fg">
          Deploy <span className="font-mono tabular-nums">#{deployId}</span>
          {app.data && (
            <span className="ml-2 font-mono text-[13px] font-normal text-fg-subtle" translate="no">
              {app.data.slug}
            </span>
          )}
        </h1>
        <Link href={back} className={cn("text-[13px]", linkCls)}>
          <span aria-hidden="true">← </span>Voltar para a aplicação
        </Link>
      </div>
    </>
  );
}

function DeployDetail({ id }: { id: number }) {
  const deploy = useDeploy(id);
  if (deploy.isPending) return <Loading label="Carregando o deploy…" />;
  if (deploy.isError) {
    const e = deploy.error;
    if (e instanceof ApiError && (e.status === 404 || e.code === "DEPLOY_NOT_FOUND" || e.code === "INVALID_DEPLOY_ID")) {
      return <DeployNotFound />;
    }
    return (
      <>
        <Breadcrumb trail={[{ label: "Projetos", href: PROJECTS_HREF }]} />
        <h1 className="sr-only">Deploy #{id}</h1>
        <Alert variant="danger" title="Não foi possível carregar o deploy.">
          <p>{e.message}</p>
          <Button size="sm" className="mt-2" loading={deploy.isFetching} onClick={() => void deploy.refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      </>
    );
  }
  return (
    <>
      <AppCrumbs appId={deploy.data.appId} deployId={id} />
      <DeployLog id={id} />
    </>
  );
}

function FromQuery() {
  const params = useSearchParams();
  const id = parseId(params.get("id"));
  if (id === null) return <DeployNotFound />;
  return <DeployDetail key={id} id={id} />;
}

export default function DeployDetailPage() {
  return (
    <Suspense fallback={<Loading label="Carregando o deploy…" />}>
      <FromQuery />
    </Suspense>
  );
}
