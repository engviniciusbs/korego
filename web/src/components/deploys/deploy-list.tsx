"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState, type ReactNode } from "react";
import { linkCls } from "@/components/apps/form-bits";
import { shortSha, statusVariant } from "@/components/apps/page-bits";
import { PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Dialog, Panel, Spinner } from "@/components/ui";
import { ApiError } from "@/lib/api";
import type { AppView } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { isActive, statusLabel, useCancelDeploy, useDeploys, useRollback, type Deploy } from "@/lib/deploys";
import { formatDateTime } from "@/lib/format";

export const deployHref = (id: number) => `/deploys/detalhe/?id=${id}`;

const TRIGGER_LABEL: Record<string, string> = { manual: "Manual", webhook: "Push", push: "Push", rollback: "Rollback" };
export const triggerLabel = (t: string) => TRIGGER_LABEL[t] ?? t;

/** Quantos deploys antigos mantêm a imagem (o servidor guarda as 3 mais recentes). */
const ROLLBACK_WINDOW = 3;

/** Duração em segundos → "42 s", "1 min 30 s", "1 h 2 min". */
export function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.round(totalSeconds));
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return s % 60 ? `${m} min ${s % 60} s` : `${m} min`;
  const h = Math.floor(m / 60);
  return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
}

/** Duração do deploy, "em andamento" ou null quando não se aplica. */
export function deployDuration(d: Pick<Deploy, "status" | "startedAt" | "finishedAt">): string | null {
  if (!d.finishedAt) return isActive(d.status) ? "em andamento" : null;
  if (!d.startedAt) return null;
  const ms = new Date(d.finishedAt).getTime() - new Date(d.startedAt).getTime();
  return Number.isFinite(ms) ? formatDuration(ms / 1000) : null;
}

/** Erros de cancelar e de rollback: as mensagens do servidor já explicam. */
export function actionErrorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return err instanceof Error ? err.message : "Erro desconhecido.";
}

/** Deploys que aceitam "Voltar para esta versão": os 3 mais recentes com imagem, fora o atual. */
export function rollbackTargets(app: AppView, deploys: Deploy[]): Set<number> {
  if (app.buildType === "compose") return new Set();
  const ids = [...deploys]
    .sort((a, b) => b.id - a.id)
    .filter((d) => (d.status === "running" || d.status === "superseded") && d.image)
    .slice(0, ROLLBACK_WINDOW)
    .map((d) => d.id)
    .filter((id) => id !== app.currentDeploy?.id);
  return new Set(ids);
}

const Dash = ({ label }: { label: string }) => (
  <>
    <span aria-hidden="true" className="text-fg-subtle">
      —
    </span>
    <span className="sr-only">{label}</span>
  </>
);

/** "Cancelar" com confirmação; some quando o deploy termina. */
export function CancelDeployButton({ deploy, size = "sm" }: { deploy: Pick<Deploy, "id">; size?: "sm" | "md" }) {
  const cancel = useCancelDeploy();
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        size={size}
        variant="ghost"
        className="text-danger hover:text-danger"
        onClick={() => {
          cancel.reset();
          setOpen(true);
        }}
      >
        Cancelar<span className="sr-only"> o deploy #{deploy.id}</span>
      </Button>
      <Dialog
        open={open}
        variant="danger"
        title={`Cancelar o deploy #${deploy.id}?`}
        confirmLabel="Cancelar deploy"
        cancelLabel="Continuar deploy"
        loading={cancel.isPending}
        onCancel={() => setOpen(false)}
        onConfirm={() => cancel.mutate(deploy.id, { onSuccess: () => setOpen(false) })}
      >
        <p>O build ou a subida para agora. A versão que está no ar continua no ar.</p>
        {cancel.isError && <Alert variant="danger">{actionErrorMessage(cancel.error)}</Alert>}
      </Dialog>
    </>
  );
}

function RollbackButton({ app, deploy }: { app: AppView; deploy: Deploy }) {
  const router = useRouter();
  const rollback = useRollback();
  const [open, setOpen] = useState(false);
  const [leaving, setLeaving] = useState(false);
  return (
    <>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => {
          rollback.reset();
          setOpen(true);
        }}
      >
        Voltar para esta versão<span className="sr-only"> (deploy #{deploy.id})</span>
      </Button>
      <Dialog
        open={open}
        title={`Voltar para o deploy #${deploy.id}?`}
        eyebrow="Rollback"
        confirmLabel="Voltar para esta versão"
        loading={rollback.isPending || leaving}
        onCancel={() => setOpen(false)}
        onConfirm={() =>
          rollback.mutate(
            { appId: app.id, deployId: deploy.id },
            {
              onSuccess: (d) => {
                setLeaving(true);
                router.push(deployHref(d.id));
              },
            },
          )
        }
      >
        <p>
          Um novo deploy sobe a imagem desta versão
          {deploy.commitSha ? (
            <>
              {" "}
              (
              <span className="font-mono text-fg" translate="no">
                {shortSha(deploy.commitSha)}
              </span>
              )
            </>
          ) : null}{" "}
          sem construir de novo. A versão atual sai do ar quando a nova ficar saudável. As variáveis usadas são as de
          agora.
        </p>
        {rollback.isError && <Alert variant="danger">{actionErrorMessage(rollback.error)}</Alert>}
      </Dialog>
    </>
  );
}

const th = "px-3 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const td = "px-3 py-2.5 align-middle";

function Row({ app, d, canRollback }: { app: AppView; d: Deploy; canRollback: boolean }) {
  const current = app.currentDeploy?.id === d.id;
  const duration = deployDuration(d);
  const started = d.startedAt ?? d.createdAt;
  return (
    <tr
      aria-label={`Deploy #${d.id}`}
      className={cn(
        "border-t border-line hover:bg-surface-2/50",
        current && "bg-success-bg/30 shadow-[inset_2px_0_0_0_var(--color-success)]",
      )}
    >
      <td className={cn(td, "whitespace-nowrap font-mono text-xs text-fg tabular-nums")}>
        <span className="flex items-center gap-2">
          #{d.id}
          {current && (
            <Badge size="sm" variant="success" data-current>
              No ar
            </Badge>
          )}
        </span>
      </td>
      <td className={td}>
        <Badge size="sm" dot variant={statusVariant(d.status)}>
          {statusLabel(d.status)}
        </Badge>
      </td>
      <td className={cn(td, "whitespace-nowrap text-xs text-fg-muted")}>{triggerLabel(d.trigger)}</td>
      <td className={cn(td, "max-w-[22rem] text-xs")}>
        {d.commitSha ? (
          <span className="flex min-w-0 items-baseline gap-2">
            <span className="shrink-0 font-mono text-fg" title={d.commitSha} translate="no">
              {shortSha(d.commitSha)}
            </span>
            {d.commitMessage && (
              <span className="min-w-0 truncate text-fg-muted" title={d.commitMessage}>
                {d.commitMessage}
              </span>
            )}
          </span>
        ) : (
          <Dash label="sem commit" />
        )}
      </td>
      <td className={cn(td, "whitespace-nowrap font-mono text-xs text-fg-muted tabular-nums")}>
        <time dateTime={started}>{formatDateTime(started)}</time>
      </td>
      <td className={cn(td, "whitespace-nowrap font-mono text-xs tabular-nums")}>
        {duration === "em andamento" ? (
          <span className="font-sans text-info">em andamento</span>
        ) : duration ? (
          <span className="text-fg-muted">{duration}</span>
        ) : (
          <Dash label="sem duração" />
        )}
      </td>
      <td className={cn(td, "whitespace-nowrap")}>
        <span className="flex items-center justify-end gap-1">
          {isActive(d.status) && <CancelDeployButton deploy={d} />}
          {canRollback && <RollbackButton app={app} deploy={d} />}
          <Link href={deployHref(d.id)} className={cn("px-2 text-xs", linkCls)}>
            Ver log<span className="sr-only"> do deploy #{d.id}</span>
          </Link>
        </span>
      </td>
    </tr>
  );
}

/** Aba Deploys da aplicação: histórico ao vivo com log, cancelar e rollback. */
export function DeployList({ app }: { app: AppView }) {
  const deploys = useDeploys(app.id);

  let body: ReactNode;
  if (deploys.isPending) {
    body = (
      <PanelState>
        <Spinner size="sm" decorative />
        <span role="status">Carregando os deploys…</span>
      </PanelState>
    );
  } else if (deploys.isError) {
    body = (
      <Alert variant="danger" title="Não foi possível carregar os deploys.">
        <p>{deploys.error.message}</p>
        <Button size="sm" className="mt-2" loading={deploys.isFetching} onClick={() => void deploys.refetch()}>
          Tentar novamente
        </Button>
      </Alert>
    );
  } else if (deploys.data.length === 0) {
    body = (
      <PanelState>
        <span>Nenhum deploy ainda. Use “Deploy agora” na aba Visão.</span>
      </PanelState>
    );
  } else {
    const targets = rollbackTargets(app, deploys.data);
    body = (
      <div className="-mx-3 -mb-3 overflow-x-auto">
        <table className="w-full min-w-[52rem] border-collapse text-[13px]">
          <caption className="sr-only">Deploys de {app.slug}, do mais recente ao mais antigo</caption>
          <thead>
            <tr>
              <th scope="col" className={th}>
                Deploy
              </th>
              <th scope="col" className={th}>
                Status
              </th>
              <th scope="col" className={th}>
                Gatilho
              </th>
              <th scope="col" className={th}>
                Commit
              </th>
              <th scope="col" className={th}>
                Início
              </th>
              <th scope="col" className={th}>
                Duração
              </th>
              <th scope="col" className={cn(th, "text-right")}>
                <span className="sr-only">Ações</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {deploys.data.map((d) => (
              <Row key={d.id} app={app} d={d} canRollback={targets.has(d.id)} />
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <Panel
      title="Deploys"
      size="sm"
      description={
        app.buildType === "compose"
          ? "Aplicações Compose não têm rollback: faça um novo deploy da versão desejada."
          : "Os 3 deploys mais recentes guardam a imagem e permitem voltar para aquela versão."
      }
    >
      {body}
    </Panel>
  );
}
