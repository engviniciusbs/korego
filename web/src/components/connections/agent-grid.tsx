"use client";

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Button } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import type { AgentClient } from "@/lib/mcp-clients";
import { ClientLogo } from "./client-logo";

/** Copia `text` e mostra "Copiado" por 2 s (também anunciado a leitores de tela). */
export function CopyButton({
  text,
  label,
  children = "Copiar",
  className,
  variant = "secondary",
}: {
  text: string;
  label: string;
  children?: ReactNode;
  className?: string;
  variant?: "secondary" | "ghost";
}) {
  const [state, setState] = useState<"idle" | "done" | "failed">("idle");
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const show = (s: "done" | "failed") => {
    clearTimeout(timer.current);
    setState(s);
    timer.current = setTimeout(() => setState("idle"), 2000);
  };
  const message = state === "done" ? "Copiado" : state === "failed" ? "Não foi possível copiar" : "";
  return (
    <>
      <Button
        size="sm"
        variant={variant}
        aria-label={label}
        className={cn(state === "done" && "text-success", state === "failed" && "text-danger", className)}
        onClick={() => {
          if (!navigator.clipboard) {
            show("failed");
            return;
          }
          navigator.clipboard.writeText(text).then(
            () => show("done"),
            () => show("failed"),
          );
        }}
      >
        {message || children}
      </Button>
      <span role="status" className="sr-only">
        {message}
      </span>
    </>
  );
}

/** Bloco de código com o botão Copiar colado à direita. */
export function CodeCopy({ text, label }: { text: string; label: string }) {
  return (
    <div className="flex w-full min-w-0 border border-line-strong bg-bg">
      <code
        className="block min-w-0 flex-1 whitespace-pre-wrap px-3 py-2 font-mono text-xs leading-relaxed text-fg [overflow-wrap:anywhere]"
        translate="no"
      >
        {text}
      </code>
      <CopyButton
        text={text}
        label={label}
        variant="ghost"
        className="h-auto shrink-0 self-stretch border-0 border-l border-line-strong px-3"
      />
    </div>
  );
}

function ExternalIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-3.5" aria-hidden="true" focusable="false">
      <path
        d="M9 2h5v5M14 2 7.5 8.5M12 9.5V13a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1h3.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
      />
    </svg>
  );
}

function Action({ client }: { client: AgentClient }) {
  const a = client.action;
  if (a.type === "link") {
    return (
      <a
        href={a.href}
        className={cn(
          "inline-flex h-9 w-full touch-manipulation items-center justify-center gap-2 border border-accent bg-accent px-3.5 text-[13px] font-medium tracking-tight text-accent-fg",
          "hover:border-accent-hover hover:bg-accent-hover motion-safe:transition-colors motion-safe:duration-100",
          focusRing,
        )}
      >
        <ExternalIcon />
        {a.label}
      </a>
    );
  }
  if (a.type === "command") {
    return <CodeCopy text={a.text} label={`Copiar comando do ${client.name}`} />;
  }
  return (
    <CopyButton text={a.text} label={`Copiar configuração do ${client.name}`} className="h-9 px-3.5 text-[13px]">
      {a.label}
    </CopyButton>
  );
}

function AgentCard({ client }: { client: AgentClient }) {
  const titleId = useId();
  return (
    <article className="flex min-w-0 flex-col gap-3.5 bg-surface px-5 py-5 sm:px-6" aria-labelledby={titleId}>
      <header className="flex items-center gap-3">
        <ClientLogo id={client.id} />
        <div className="min-w-0">
          <h3 id={titleId} className="text-sm font-semibold tracking-tight text-fg">
            {client.name}
          </h3>
          <p className="font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle">{client.kind}</p>
        </div>
      </header>
      <div className="flex min-h-9 items-stretch">
        <Action client={client} />
      </div>
      <p className="text-[12.5px] leading-relaxed text-fg-muted">{client.note}</p>
      {client.manual && (
        <details className="group">
          <summary
            className={cn(
              "cursor-pointer list-none text-xs text-fg-subtle hover:text-fg-muted [&::-webkit-details-marker]:hidden",
              focusRing,
            )}
          >
            <span className="inline-block w-3 group-open:rotate-90 motion-safe:transition-transform" aria-hidden="true">
              ▸
            </span>
            Configuração manual
          </summary>
          <div className="mt-2.5 space-y-1.5">
            <p className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">{client.manual.label}</p>
            <CodeCopy text={client.manual.text} label={`Copiar configuração manual do ${client.name}`} />
          </div>
        </details>
      )}
    </article>
  );
}

/** Grade de cartões, um por cliente, separados por linhas de 1px. */
export function AgentGrid({
  clients,
  compact = false,
  className,
}: {
  clients: AgentClient[];
  /** Uma coluna só, para caber em diálogos. */
  compact?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("grid grid-cols-1 gap-px bg-line", !compact && "sm:grid-cols-2 lg:grid-cols-3", className)}>
      {clients.map((c) => (
        <AgentCard key={c.id} client={c} />
      ))}
    </div>
  );
}
