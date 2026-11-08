"use client";

import Link from "next/link";
import { useState } from "react";
import { CertStatus } from "@/components/domains/cert-status";
import { DomainDialog, WWW_OPTIONS } from "@/components/domains/domain-dialog";
import { ProxyBanner } from "@/components/domains/proxy-banner";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Dialog, Panel, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import { useDeleteDomain, useDomains, type Domain } from "@/lib/domains";

const th = "px-3 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const td = "px-3 py-2.5 align-middle";
const link = cn("text-accent-text hover:text-fg hover:underline", focusRing);

const wwwLabel = (v: string) => WWW_OPTIONS.find((o) => o.value === v)?.label ?? v;

function Target({ d }: { d: Domain }) {
  if (d.isPanel) {
    return <span className="font-mono text-[13px] text-fg-muted">painel KoreGO</span>;
  }
  if (d.targetType === "app" && d.appId) {
    return (
      <span className="text-[13px]">
        <span className="sr-only">Aplicação </span>
        <Link href={`/aplicacoes/detalhe/?id=${d.appId}`} translate="no" className={cn("font-mono", link)}>
          {d.appSlug || `#${d.appId}`}
        </Link>
      </span>
    );
  }
  return (
    <span className="font-mono text-[13px] text-fg [overflow-wrap:anywhere]" translate="no">
      {d.address}
    </span>
  );
}

function Options({ d }: { d: Domain }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      <Badge size="sm" variant={d.forceHttps ? "accent" : "neutral"}>
        {d.forceHttps ? "HTTPS forçado" : "HTTP aceito"}
      </Badge>
      {d.wwwRedirect && d.wwwRedirect !== "none" && <Badge size="sm">{wwwLabel(d.wwwRedirect)}</Badge>}
    </div>
  );
}

function Row({ d, onEdit, onRemove }: { d: Domain; onEdit: () => void; onRemove: () => void }) {
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
          <Link href="/configuracoes/painel/" className={cn("whitespace-nowrap text-xs", link)}>
            Gerenciar em Configurações <span aria-hidden="true">→</span>
          </Link>
        ) : (
          <div className="flex items-center justify-end gap-1.5">
            <Button size="sm" onClick={onEdit}>
              Editar<span className="sr-only"> {d.hostname}</span>
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="enabled:hover:bg-danger-bg enabled:hover:text-danger"
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
          <Button variant="primary" onClick={() => setEditing({ mode: "new" })}>
            Adicionar domínio
          </Button>
        }
      />

      <div className="space-y-4">
        <ProxyBanner />

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
