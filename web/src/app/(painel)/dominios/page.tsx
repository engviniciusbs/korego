"use client";

import Link from "next/link";
import { useState } from "react";
import { CertStatus } from "@/components/domains/cert-status";
import { CertificatesPanel, formatCertDate } from "@/components/domains/certificates-panel";
import { DomainDialog, WWW_OPTIONS } from "@/components/domains/domain-dialog";
import { MIGRATING_TITLE, ProxyBanner } from "@/components/domains/proxy-banner";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Dialog, Panel, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import { domainRoutes, useDeleteDomain, useDomains, useMigrating, type Domain } from "@/lib/domains";

const th = "px-3 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const td = "px-3 py-2.5 align-middle";
const link = cn("text-accent-text hover:text-fg hover:underline", focusRing);

const wwwLabel = (v: string) => WWW_OPTIONS.find((o) => o.value === v)?.label ?? v;

/** Destino da rota "/" e, com rotas por caminho, quantas há. */
function Target({ d }: { d: Domain }) {
  if (d.isPanel) {
    return <span className="font-mono text-[13px] text-fg-muted">painel KoreGO</span>;
  }
  const routes = domainRoutes(d);
  const root = routes.find((r) => r.path === "/") ?? routes[routes.length - 1];
  const extra = routes.length - 1;
  let main;
  if (root.targetType === "app" && root.appId) {
    main = (
      <span className="text-[13px]">
        <span className="sr-only">Aplicação </span>
        <Link href={`/aplicacoes/detalhe/?id=${root.appId}`} translate="no" className={cn("font-mono", link)}>
          {(root.appId === d.appId && d.appSlug) || `#${root.appId}`}
        </Link>
      </span>
    );
  } else {
    main = (
      <span className="font-mono text-[13px] text-fg [overflow-wrap:anywhere]" translate="no">
        {root.targetType === "static" ? (
          <>
            <span className="sr-only">Pasta </span>
            {root.root}
          </>
        ) : (
          root.address
        )}
      </span>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {main}
      {extra > 0 && (
        <Badge size="sm" title={routes.map((r) => r.path).join("  ")}>
          +{extra} rota{extra === 1 ? "" : "s"}
          <span className="sr-only">
            {" "}
            por caminho: {routes.filter((r) => r.path !== "/").map((r) => r.path).join(", ")}
          </span>
        </Badge>
      )}
    </div>
  );
}

function Options({ d }: { d: Domain }) {
  const custom = d.tls?.mode === "custom";
  const expiry = custom && d.tls.expiresSoon ? formatCertDate(d.tls.notAfter) : "";
  return (
    <div className="flex flex-wrap gap-1.5">
      {!d.isPanel && (
        <Badge size="sm" variant={d.forceHttps ? "accent" : "neutral"}>
          {d.forceHttps ? "HTTPS forçado" : "HTTP aceito"}
        </Badge>
      )}
      {!d.isPanel && d.wwwRedirect && d.wwwRedirect !== "none" && <Badge size="sm">{wwwLabel(d.wwwRedirect)}</Badge>}
      {custom && (
        <Badge size="sm" variant="info" title={d.tls.certName || undefined}>
          Certificado próprio
        </Badge>
      )}
      {d.cloudflareOnly && (
        <Badge size="sm" variant="info">
          Só Cloudflare
        </Badge>
      )}
      {custom && d.tls.expiresSoon && (
        <Badge size="sm" variant="warning" dot>
          {expiry ? `Vence em ${expiry}` : "Vence em breve"}
        </Badge>
      )}
    </div>
  );
}

function Row({
  d,
  locked,
  onEdit,
  onRemove,
}: {
  d: Domain;
  /** Troca de servidor em andamento: editar e remover ficam bloqueados. */
  locked: boolean;
  onEdit: () => void;
  onRemove: () => void;
}) {
  return (
    <tr className="border-t border-line motion-safe:transition-colors hover:bg-surface-2/60">
      <td className={cn(td, "min-w-48")}>
        <div className="flex flex-wrap items-center gap-2">
          <a
            href={`https://${d.hostname}`}
            target="_blank"
            rel="noopener noreferrer"
            translate="no"
            className={cn(
              "font-mono text-[13px] font-medium text-fg [overflow-wrap:anywhere] hover:text-accent-text hover:underline",
              focusRing,
            )}
          >
            <span>{d.hostname}</span>
            <span className="sr-only"> (abre em nova aba)</span>
          </a>
          {d.isPanel && (
            <Badge size="sm" variant="accent">
              Painel
            </Badge>
          )}
        </div>
      </td>
      <td className={td}>
        <Target d={d} />
      </td>
      <td className={cn(td, "max-w-72")}>
        <CertStatus cert={d.cert} />
      </td>
      <td className={td}>
        <Options d={d} />
      </td>
      <td className={cn(td, "text-right")}>
        {d.isPanel ? (
          <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1.5">
            <Button size="sm" disabled={locked} onClick={onEdit}>
              Editar<span className="sr-only"> {d.hostname}</span>
            </Button>
            <Link href="/configuracoes/painel/" className={cn("whitespace-nowrap text-xs", link)}>
              Hostname em Configurações
            </Link>
          </div>
        ) : (
          <div className="flex items-center justify-end gap-1.5">
            <Button size="sm" disabled={locked} onClick={onEdit}>
              Editar<span className="sr-only"> {d.hostname}</span>
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="enabled:hover:bg-danger-bg enabled:hover:text-danger"
              disabled={locked}
              onClick={onRemove}
            >
              Remover<span className="sr-only"> {d.hostname}</span>
            </Button>
          </div>
        )}
      </td>
    </tr>
  );
}

type Editing = { mode: "new" } | { mode: "edit"; domain: Domain } | null;

export default function DominiosPage() {
  const domains = useDomains();
  const remove = useDeleteDomain();
  const migrating = useMigrating();
  const [editing, setEditing] = useState<Editing>(null);
  const [removing, setRemoving] = useState<Domain | null>(null);

  // O domínio do painel primeiro; depois em ordem alfabética.
  const list = [...(domains.data ?? [])].sort(
    (a, b) => Number(b.isPanel) - Number(a.isPanel) || a.hostname.localeCompare(b.hostname),
  );

  const confirmRemove = () => {
    if (!removing) return;
    remove.mutate(removing.id, { onSuccess: () => setRemoving(null) });
  };

  return (
    <>
      <PageHeader
        eyebrow="Publicar"
        title="Domínios"
        description="Domínios servidos pelo proxy do KoreGO, com HTTPS automático."
        actions={
          <div className="flex flex-wrap items-center justify-end gap-3">
            {migrating && (
              <span role="status" className="text-xs text-fg-subtle">
                {MIGRATING_TITLE}
              </span>
            )}
            <Button variant="primary" disabled={migrating} onClick={() => setEditing({ mode: "new" })}>
              Adicionar domínio
            </Button>
          </div>
        }
      />

      <div className="space-y-4">
        <ProxyBanner detailed />

        <Panel
          title="Domínios"
          description={domains.isSuccess ? `${list.length} cadastrado${list.length === 1 ? "" : "s"}` : undefined}
          size="sm"
          className="[&>div]:p-0"
        >
          {domains.isPending ? (
            <PanelState>
              <Spinner size="sm" decorative />
              <span role="status">Carregando domínios…</span>
            </PanelState>
          ) : domains.isError ? (
            <div className="p-3">
              <Alert variant="danger" title="Não foi possível listar os domínios.">
                <p>{domains.error.message}</p>
                <Button size="sm" className="mt-2" onClick={() => void domains.refetch()}>
                  Tentar novamente
                </Button>
              </Alert>
            </div>
          ) : list.length === 0 ? (
            <PanelState>
              <span>Nenhum domínio cadastrado.</span>
            </PanelState>
          ) : (
            // `relative`: os textos sr-only (absolutos) das células ficam presos à rolagem e não alargam a página.
            <div className="relative overflow-x-auto">
              <table className="w-full min-w-[720px] text-left">
                <thead>
                  <tr>
                    <th scope="col" className={th}>Hostname</th>
                    <th scope="col" className={th}>Destino</th>
                    <th scope="col" className={th}>SSL</th>
                    <th scope="col" className={th}>Opções</th>
                    <th scope="col" className={cn(th, "text-right")}>
                      <span className="sr-only">Ações</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {list.map((d) => (
                    <Row
                      key={d.id}
                      d={d}
                      locked={migrating}
                      onEdit={() => setEditing({ mode: "edit", domain: d })}
                      onRemove={() => {
                        remove.reset();
                        setRemoving(d);
                      }}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>

        <CertificatesPanel />
      </div>

      {editing && (
        <DomainDialog
          key={editing.mode === "edit" ? editing.domain.id : "new"}
          domain={editing.mode === "edit" ? editing.domain : undefined}
          onClose={() => setEditing(null)}
        />
      )}

      <Dialog
        open={removing !== null}
        title="Remover domínio?"
        confirmLabel="Remover"
        variant="danger"
        loading={remove.isPending}
        onConfirm={confirmRemove}
        onCancel={() => setRemoving(null)}
      >
        <p>
          <span className="font-mono text-fg" translate="no">
            {removing?.hostname}
          </span>{" "}
          deixa de ser servido pelo proxy. O certificado é descartado.
        </p>
        {remove.isError && <Alert variant="danger">{remove.error.message}</Alert>}
      </Dialog>
    </>
  );
}
