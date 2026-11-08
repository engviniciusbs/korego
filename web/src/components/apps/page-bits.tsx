"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { PanelState } from "@/components/shell/page-header";
import { Badge, Panel, Spinner, type BadgeVariant } from "@/components/ui";
import { ApiError } from "@/lib/api";
import type { AppView } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { isActive, statusLabel } from "@/lib/deploys";
import { linkCls } from "./form-bits";

export const PROJECTS_HREF = "/projetos/";
export const projectHref = (id: number) => `/projetos/detalhe/?id=${id}`;
export const appHref = (id: number, aba?: string) => `/aplicacoes/detalhe/?id=${id}${aba ? `&aba=${aba}` : ""}`;

/** IDs numéricos positivos vindos da query string. */
export function parseId(v: string | null): number | null {
  if (!v || !/^[1-9]\d{0,15}$/.test(v)) return null;
  return Number(v);
}

export interface Crumb {
  label: string;
  href?: string;
}

export function Breadcrumb({ trail }: { trail: Crumb[] }) {
  return (
    <nav aria-label="Trilha" className="mb-3 font-mono text-[11px] uppercase tracking-[0.12em] text-fg-subtle">
      <ol className="flex flex-wrap items-center gap-y-1">
        {trail.map((c, i) => {
          const last = i === trail.length - 1;
          return (
            <li key={`${i}-${c.label}`} className="flex min-w-0 items-center">
              {i > 0 && (
                <span aria-hidden="true" className="mx-2">
                  /
                </span>
              )}
              {c.href && !last ? (
                <Link href={c.href} className={linkCls}>
                  {c.label}
                </Link>
              ) : (
                <span
                  aria-current={last ? "page" : undefined}
                  translate={i > 0 ? "no" : undefined}
                  className={cn(i > 0 && "normal-case tracking-normal", "[overflow-wrap:anywhere]")}
                >
                  {c.label}
                </span>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

export function NotFound({ title, children }: { title: string; children: ReactNode }) {
  return (
    <>
      <Breadcrumb trail={[{ label: "Projetos", href: PROJECTS_HREF }]} />
      <Panel variant="danger">
        <div className="space-y-2 py-4 text-center">
          <h1 className="text-base font-semibold text-fg">{title}</h1>
          <p className="text-[13px] text-fg-muted">{children}</p>
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

export function Loading({ label }: { label: string }) {
  return (
    <Panel>
      <PanelState>
        <Spinner size="sm" decorative />
        <span role="status">{label}</span>
      </PanelState>
    </Panel>
  );
}

export function statusVariant(status: string): BadgeVariant {
  if (status === "running") return "success";
  if (status === "failed") return "danger";
  if (isActive(status)) return "info";
  return "neutral";
}

/** Status do deploy atual da aplicação. */
export function AppStatus({ app }: { app: AppView }) {
  const d = app.currentDeploy;
  if (!d) {
    return (
      <Badge size="sm" variant="neutral">
        Sem deploy
      </Badge>
    );
  }
  return (
    <Badge size="sm" dot variant={statusVariant(d.status)}>
      {statusLabel(d.status)}
    </Badge>
  );
}

export const shortSha = (sha: string) => sha.slice(0, 7);

export function DomainLinks({ domains, empty }: { domains: string[]; empty?: ReactNode }) {
  if (domains.length === 0) return empty ? <>{empty}</> : null;
  return (
    <ul className="flex flex-wrap gap-x-3 gap-y-1" translate="no">
      {domains.map((h) => (
        <li key={h} className="min-w-0">
          <a
            href={`https://${h}`}
            target="_blank"
            rel="noopener noreferrer"
            className={cn("font-mono text-xs [overflow-wrap:anywhere]", linkCls)}
          >
            <span>{h}</span>
            <span className="sr-only"> (abre em nova aba)</span>
          </a>
        </li>
      ))}
    </ul>
  );
}

/** GitHub desconectado, com o caminho para conectar. */
export function NoGitHubMessage() {
  return (
    <>
      O GitHub não está conectado.{" "}
      <Link href="/configuracoes/github/" className={linkCls}>
        Conecte em Configurações → GitHub
      </Link>
      .
    </>
  );
}

/** Mensagem de erro ao iniciar um deploy. */
export function deployErrorMessage(err: unknown): ReactNode {
  if (!(err instanceof ApiError)) return err instanceof Error ? err.message : "Erro desconhecido.";
  switch (err.code) {
    case "NO_GITHUB":
      return <NoGitHubMessage />;
    case "APP_DELETING":
      return "A aplicação está sendo removida.";
    default:
      return err.message;
  }
}

/** Origem resumida: repositório e branch, ou a imagem. */
export function sourceSummary(app: AppView): string {
  if (app.sourceType === "image") return app.image;
  return app.branch ? `${app.repo} @ ${app.branch}` : app.repo;
}

/** Botão secundário com aparência de link-botão do kit. */
export const buttonLinkCls = (primary = false) =>
  cn(
    "inline-flex h-8 touch-manipulation items-center justify-center gap-2 whitespace-nowrap border px-3 text-[13px] font-medium tracking-tight",
    "motion-safe:transition-colors motion-safe:duration-100",
    "outline-hidden focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text",
    primary
      ? "border-accent bg-accent text-accent-fg hover:border-accent-hover hover:bg-accent-hover"
      : "border-line-strong bg-surface-2 text-fg hover:border-fg-subtle hover:bg-surface-3",
  );
