"use client";

import Link from "next/link";
import { useId } from "react";
import { Badge, type BadgeVariant } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import type { Container, ContainerGroup, PortBinding } from "@/lib/containers";
import { ContainerActions } from "./container-actions";

const stateVariant: Record<string, BadgeVariant> = {
  running: "success",
  paused: "warning",
  restarting: "warning",
  removing: "warning",
  exited: "neutral",
  created: "neutral",
  dead: "danger",
};

const healthVariant: Record<string, BadgeVariant> = {
  healthy: "success",
  unhealthy: "danger",
  starting: "warning",
};

/** `ip:public→private/proto`; sem porta pública, só `private/proto`. */
export function formatPort(p: PortBinding): string {
  const inner = `${p.privatePort}/${p.proto}`;
  if (!p.publicPort) return inner;
  return `${p.ip ? `${p.ip}:` : ""}${p.publicPort}→${inner}`;
}

const th = "px-3 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const td = "px-3 py-2.5 align-middle";

function Row({ c }: { c: Container }) {
  const ports = [...new Set(c.ports.map(formatPort))];
  return (
    <tr className="border-t border-line motion-safe:transition-colors hover:bg-surface-2/60">
      <td className={cn(td, "min-w-40")}>
        <Link
          href={`/containers/detalhe/?id=${c.id}`}
          translate="no"
          className={cn(
            "font-mono text-[13px] font-medium text-fg [overflow-wrap:anywhere] hover:text-accent-text hover:underline",
            focusRing,
          )}
        >
          {c.name}
        </Link>
        <p className="mt-0.5 font-mono text-[11px] text-fg-subtle" translate="no">
          {c.id.slice(0, 12)}
          {c.service && <span> · {c.service}</span>}
        </p>
      </td>
      <td className={td}>
        <Badge size="sm" variant={c.managed ? "accent" : "neutral"}>
          {c.managed ? "gerenciado" : "externo"}
        </Badge>
      </td>
      <td className={td}>
        <Badge size="sm" dot variant={stateVariant[c.state] ?? "neutral"}>
          {c.state}
        </Badge>
        {c.status && <p className="mt-1 whitespace-nowrap text-[11px] text-fg-subtle">{c.status}</p>}
      </td>
      <td className={td}>
        {c.health ? (
          <Badge size="sm" variant={healthVariant[c.health] ?? "neutral"}>
            {c.health}
          </Badge>
        ) : (
          <>
            <span aria-hidden="true" className="text-fg-subtle">
              —
            </span>
            <span className="sr-only">sem health check</span>
          </>
        )}
      </td>
      <td className={cn(td, "max-w-56")}>
        <span className="block truncate font-mono text-xs text-fg-muted" title={c.image} translate="no">
          {c.image}
        </span>
      </td>
      <td className={td}>
        {ports.length === 0 ? (
          <>
            <span aria-hidden="true" className="text-fg-subtle">
              —
            </span>
            <span className="sr-only">nenhuma porta</span>
          </>
        ) : (
          <ul className="space-y-0.5 font-mono text-xs tabular-nums text-fg-muted" translate="no">
            {ports.map((p) => (
              <li key={p} className="whitespace-nowrap">
                {p}
              </li>
            ))}
          </ul>
        )}
      </td>
      <td className={cn(td, "text-right")}>
        <ContainerActions container={c} />
      </td>
    </tr>
  );
}

/** Um grupo (projeto compose ou "Avulsos"): cabeçalho com nome e contagem e a tabela. */
export function ContainerTable({ group }: { group: ContainerGroup }) {
  const headingId = useId();
  const n = group.containers.length;
  const running = group.containers.filter((c) => c.state === "running").length;
  return (
    <section
      aria-labelledby={headingId}
      className={cn(
        "border border-line bg-surface",
        group.project ? "shadow-[inset_2px_0_0_0_var(--color-accent)]" : "",
      )}
    >
      <header className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-line px-4 py-2.5">
        <h2 id={headingId} className="flex items-baseline gap-3 text-sm font-semibold tracking-tight text-fg">
          <span translate={group.project ? "no" : undefined}>{group.label}</span>{" "}
          <span className="font-mono text-[11px] font-normal text-fg-subtle">
            {n} {n === 1 ? "container" : "containers"}
          </span>
        </h2>
        <p className="font-mono text-[11px] tabular-nums text-fg-subtle">
          {group.project ? "projeto compose" : "sem projeto"} · {running} rodando
        </p>
      </header>
      {/* `relative`: os textos sr-only (absolutos) das células ficam presos à rolagem e não alargam a página. */}
      <div className="relative overflow-x-auto">
        <table className="w-full min-w-[56rem] text-[13px]">
          <caption className="sr-only">Containers de {group.label}</caption>
          <thead>
            <tr>
              <th scope="col" className={th}>Nome</th>
              <th scope="col" className={th}>Origem</th>
              <th scope="col" className={th}>Estado</th>
              <th scope="col" className={th}>Health</th>
              <th scope="col" className={th}>Imagem</th>
              <th scope="col" className={th}>Portas</th>
              <th scope="col" className={cn(th, "text-right")}>
                Ações
              </th>
            </tr>
          </thead>
          <tbody>
            {group.containers.map((c) => (
              <Row key={c.id} c={c} />
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
