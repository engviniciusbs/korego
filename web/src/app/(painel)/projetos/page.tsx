"use client";

import Link from "next/link";
import { useState } from "react";
import { linkCls } from "@/components/apps/form-bits";
import { projectHref } from "@/components/apps/page-bits";
import { ProjectDialog } from "@/components/apps/project-dialog";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Button, Dialog, Panel, Spinner } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { useDeleteProject, useProjects, type Project } from "@/lib/projects";

const countLabel = (n: number) => `${n} ${n === 1 ? "aplicação" : "aplicações"}`;
const NOT_EMPTY = "Remova as aplicações do projeto antes.";

function ProjectCard({ p, onRemove }: { p: Project; onRemove: () => void }) {
  const busy = p.appCount > 0;
  const hintId = `projeto-${p.id}-hint`;
  return (
    <li className="group flex min-w-0 flex-col border border-line bg-surface motion-safe:transition-colors hover:border-line-strong">
      <div className="flex-1 space-y-1.5 px-4 pt-3.5 pb-3 shadow-[inset_2px_0_0_0_var(--color-line-strong)] group-hover:shadow-[inset_2px_0_0_0_var(--color-accent)]">
        <h2 className="text-[15px] font-semibold tracking-tight">
          <Link href={projectHref(p.id)} className={cn("text-fg [overflow-wrap:anywhere] hover:text-accent-text", linkCls)}>
            {p.name}
          </Link>
        </h2>
        {p.description ? (
          <p className="text-[13px] text-fg-muted [overflow-wrap:anywhere]">{p.description}</p>
        ) : (
          <p className="text-[13px] text-fg-subtle">Sem descrição.</p>
        )}
      </div>
      <div className="flex items-center justify-between gap-3 border-t border-line px-4 py-2">
        <span className="font-mono text-[11px] uppercase tracking-[0.08em] text-fg-muted tabular-nums">
          {countLabel(p.appCount)}
        </span>
        <div className="flex min-w-0 items-center gap-2">
          {busy && (
            <span id={hintId} className="text-right text-[11px] text-fg-subtle">
              {NOT_EMPTY}
            </span>
          )}
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            aria-describedby={busy ? hintId : undefined}
            className="enabled:hover:bg-danger-bg enabled:hover:text-danger"
            onClick={onRemove}
          >
            Remover<span className="sr-only"> o projeto {p.name}</span>
          </Button>
        </div>
      </div>
    </li>
  );
}

export default function ProjetosPage() {
  const projects = useProjects();
  const remove = useDeleteProject();
  const [creating, setCreating] = useState(false);
  const [removing, setRemoving] = useState<Project | null>(null);

  const list = [...(projects.data ?? [])].sort((a, b) => a.name.localeCompare(b.name, "pt-BR"));

  const confirmRemove = () => {
    if (!removing) return;
    remove.mutate(removing.id, { onSuccess: () => setRemoving(null) });
  };

  const removeError =
    remove.error instanceof ApiError && remove.error.code === "PROJECT_NOT_EMPTY" ? NOT_EMPTY : remove.error?.message;

  return (
    <>
      <PageHeader
        eyebrow="Publicar"
        title="Projetos"
        description="Agrupe as aplicações publicadas neste servidor."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            Novo projeto
          </Button>
        }
      />

      {projects.isPending ? (
        <Panel>
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">Carregando projetos…</span>
          </PanelState>
        </Panel>
      ) : projects.isError ? (
        <Alert variant="danger" title="Não foi possível listar os projetos.">
          <p>{projects.error.message}</p>
          <Button size="sm" className="mt-2" loading={projects.isFetching} onClick={() => void projects.refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      ) : list.length === 0 ? (
        <Panel variant="accent">
          <div className="space-y-3 py-6 text-center">
            <p className="text-sm font-medium text-fg">Nenhum projeto ainda.</p>
            <p className="text-[13px] text-fg-muted">
              Crie um projeto para publicar a primeira aplicação a partir do GitHub, de uma URL Git ou de uma imagem.
            </p>
            <Button variant="primary" onClick={() => setCreating(true)}>
              Criar o primeiro projeto
            </Button>
          </div>
        </Panel>
      ) : (
        <ul aria-label="Projetos" className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {list.map((p) => (
            <ProjectCard
              key={p.id}
              p={p}
              onRemove={() => {
                remove.reset();
                setRemoving(p);
              }}
            />
          ))}
        </ul>
      )}

      {creating && <ProjectDialog onClose={() => setCreating(false)} />}

      <Dialog
        open={removing !== null}
        title="Remover projeto?"
        confirmLabel="Remover"
        variant="danger"
        loading={remove.isPending}
        onConfirm={confirmRemove}
        onCancel={() => setRemoving(null)}
      >
        <p>
          O projeto{" "}
          <span className="font-semibold text-fg" translate="no">
            {removing?.name}
          </span>{" "}
          será apagado.
        </p>
        {remove.isError && <Alert variant="danger">{removeError}</Alert>}
      </Dialog>
    </>
  );
}
