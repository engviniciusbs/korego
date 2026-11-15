"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense } from "react";
import { linkCls } from "@/components/apps/form-bits";
import { Breadcrumb, Loading, NotFound, PROJECTS_HREF } from "@/components/apps/page-bits";
import { CertStatus } from "@/components/domains/cert-status";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { siteOrigin } from "@/components/sites/site-card";
import { Alert, Badge, Button, Panel } from "@/components/ui";
import { cn } from "@/lib/cn";
import { formatDateTime } from "@/lib/format";
import { SITE_KIND_LABEL, findSite, parseSiteKind, rootVersion, useDetectedSites, type DetectedSite } from "@/lib/sites";

function routeTarget(r: DetectedSite["domains"][number]["routes"][number]): string {
  if (r.targetType === "static") return r.root ?? "";
  return r.address ?? "";
}

function Domains({ s }: { s: DetectedSite }) {
  return (
    <Panel title="Domínios" size="sm" className="[&>div]:p-0">
      {s.domains.length === 0 ? (
        <PanelState>Nenhum domínio aponta para este site.</PanelState>
      ) : (
        <ul aria-label={`Domínios de ${s.name}`}>
          {s.domains.map((d) => (
            <li key={d.id} className="space-y-1.5 border-t border-line px-4 py-3 first:border-t-0">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <a
                  href={`https://${d.hostname}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  translate="no"
                  className={cn("font-mono text-[13px] font-medium [overflow-wrap:anywhere]", linkCls)}
                >
                  {d.hostname}
                  <span className="sr-only"> (abre em nova aba)</span>
                </a>
                <CertStatus cert={d.cert} />
                <Link href="/dominios/" className={cn("text-xs", linkCls)}>
                  Editar em Domínios<span className="sr-only"> ({d.hostname})</span>
                </Link>
              </div>
              <ul className="space-y-0.5 font-mono text-[11px] text-fg-subtle" translate="no">
                {d.routes.map((r) => (
                  <li key={r.path} className="[overflow-wrap:anywhere]">
                    {r.path} → {routeTarget(r)}
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function Containers({ s }: { s: DetectedSite }) {
  return (
    <Panel title="Containers" size="sm" className="[&>div]:p-0">
      <ul aria-label={`Containers de ${s.name}`}>
        {s.containers.map((c) => (
          <li
            key={c.id}
            className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-line px-4 py-3 first:border-t-0"
          >
            <div className="min-w-0 space-y-0.5">
              <Link
                href={`/containers/detalhe/?id=${c.id}`}
                translate="no"
                className={cn("font-mono text-[13px] font-medium text-fg hover:text-accent-text", linkCls)}
              >
                {c.service || c.name}
              </Link>
              <p className="truncate font-mono text-[11px] text-fg-subtle" translate="no" title={c.image}>
                {c.name} · {c.image.startsWith("sha256:") ? c.image.slice(0, 19) : c.image}
              </p>
            </div>
            <div className="flex items-center gap-2">
              <Badge size="sm" dot variant={c.state === "running" ? "success" : "warning"}>
                {c.state === "running" ? "Rodando" : c.state}
              </Badge>
              <span className="text-xs text-fg-subtle">{c.status}</span>
            </div>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

function Folders({ s }: { s: DetectedSite }) {
  return (
    <Panel title="Pasta" size="sm" className="[&>div]:p-0">
      <ul aria-label={`Pastas de ${s.name}`}>
        {s.roots.map((r) => (
          <li key={r.path} className="space-y-1 border-t border-line px-4 py-3 first:border-t-0">
            <p className="font-mono text-[13px] text-fg [overflow-wrap:anywhere]" translate="no">
              {r.root}
            </p>
            {r.exists ? (
              <dl className="grid gap-x-4 gap-y-1 text-[13px] sm:grid-cols-[auto_minmax(0,1fr)]">
                <dt className="text-fg-muted">Versão atual</dt>
                <dd className="font-mono text-[12px] [overflow-wrap:anywhere]" translate="no">
                  {rootVersion(r)}
                </dd>
                <dt className="text-fg-muted">Caminho real</dt>
                <dd className="font-mono text-[12px] [overflow-wrap:anywhere]" translate="no">
                  {r.resolved}
                </dd>
                {r.modifiedAt && (
                  <>
                    <dt className="text-fg-muted">Publicada em</dt>
                    <dd>{formatDateTime(r.modifiedAt)}</dd>
                  </>
                )}
              </dl>
            ) : (
              <p className="text-[13px] text-warning">Pasta não encontrada. O site responde com erro até ela existir.</p>
            )}
          </li>
        ))}
      </ul>
    </Panel>
  );
}

function SiteView({ s }: { s: DetectedSite }) {
  return (
    <>
      <Breadcrumb trail={[{ label: "Projetos", href: PROJECTS_HREF }, { label: s.name }]} />
      <PageHeader
        eyebrow={SITE_KIND_LABEL[s.kind]}
        title={s.name}
        description={`${siteOrigin(s)}. Esta página é só de leitura.`}
      />
      <div className="space-y-4">
        {s.roots.length > 0 && <Folders s={s} />}
        {s.containers.length > 0 && <Containers s={s} />}
        <Domains s={s} />
      </div>
    </>
  );
}

function FromQuery() {
  const q = useSearchParams();
  const kind = parseSiteKind(q.get("tipo"));
  const name = q.get("nome") ?? "";
  const sites = useDetectedSites();
  const notFound = (
    <NotFound title="Site não encontrado">Ele pode ter sido parado ou removido, ou o endereço está incompleto.</NotFound>
  );
  if (!kind || !name) return notFound;
  if (sites.isPending) return <Loading label="Carregando o site…" />;
  if (sites.isError) {
    return (
      <>
        <Breadcrumb trail={[{ label: "Projetos", href: PROJECTS_HREF }]} />
        <h1 className="sr-only">Site</h1>
        <Alert variant="danger" title="Não foi possível carregar o site.">
          <p>{sites.error?.message}</p>
          <Button size="sm" className="mt-2" loading={sites.isFetching} onClick={sites.refetch}>
            Tentar novamente
          </Button>
        </Alert>
      </>
    );
  }
  const s = findSite(sites.data ?? [], kind, name);
  if (!s) return notFound;
  return <SiteView s={s} />;
}

export default function SitePage() {
  return (
    <Suspense fallback={<Loading label="Carregando o site…" />}>
      <FromQuery />
    </Suspense>
  );
}
