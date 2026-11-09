"use client";

import { useId, useMemo, useState } from "react";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import type { UsageOwnerLive } from "@/lib/usage";
import { OwnerRow } from "./owner-row";

export type UsageSort = "cpu" | "mem";

export const RAM_HINT =
  "Soma da memória residente dos processos; páginas compartilhadas podem ser contadas mais de uma vez.";

const th = "px-3 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";

/** Ordena do maior para o menor pela métrica; empate pelo nome. */
export function sortOwners<T extends UsageOwnerLive>(list: T[], by: UsageSort): T[] {
  const v = (o: T) => (by === "cpu" ? o.cpuPercent : o.memBytes);
  return [...list].sort((a, b) => v(b) - v(a) || a.label.localeCompare(b.label, "pt-BR"));
}

function SortButton({
  active,
  onClick,
  describedBy,
  children,
}: {
  active: boolean;
  onClick: () => void;
  describedBy?: string;
  children: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-describedby={describedBy}
      className={cn(
        "inline-flex min-h-6 touch-manipulation items-center gap-1.5 uppercase tracking-[0.12em] hover:text-fg",
        active ? "text-fg" : "text-fg-subtle",
        focusRing,
      )}
    >
      {children}
      {/* Seta só visual: o estado vai em aria-sort no cabeçalho. */}
      <span
        aria-hidden="true"
        className={cn(
          "size-1.5 border-r-[1.5px] border-b-[1.5px] border-current rotate-45 -translate-y-0.5",
          !active && "opacity-0",
        )}
      />
    </button>
  );
}

export interface OwnerTableProps {
  owners: UsageOwnerLive[];
  memTotal: number | undefined;
}

/** Tabela de donos: ordenável por CPU ou RAM; cada linha abre seus processos e histórico. */
export function OwnerTable({ owners, memTotal }: OwnerTableProps) {
  const [sort, setSort] = useState<UsageSort>("cpu");
  const [open, setOpen] = useState<ReadonlySet<string>>(() => new Set());
  const hintId = useId();
  // Esc esconde a dica até o ponteiro ou o foco saírem (WCAG 1.4.13).
  const [hintOff, setHintOff] = useState(false);

  const rows = useMemo(() => sortOwners(owners, sort), [owners, sort]);

  const toggle = (key: string) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (!next.delete(key)) next.add(key);
      return next;
    });

  const sortAttr = (s: UsageSort) => (sort === s ? ("descending" as const) : undefined);

  return (
    // `relative`: textos sr-only e a dica ficam presos à rolagem e não alargam a página.
    <div className="relative overflow-x-auto">
      <table className="w-full min-w-[40rem] text-[13px]">
        <caption className="sr-only">
          Consumo por dono, ordenado por {sort === "cpu" ? "CPU" : "RAM"} (maior primeiro)
        </caption>
        <colgroup>
          <col />
          <col className="w-[22%]" />
          <col className="w-[22%]" />
          <col className="w-24" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col" className={th}>
              Dono
            </th>
            <th scope="col" aria-sort={sortAttr("cpu")} className={cn(th, "text-right")}>
              <SortButton active={sort === "cpu"} onClick={() => setSort("cpu")}>
                CPU
              </SortButton>
            </th>
            <th scope="col" aria-sort={sortAttr("mem")} className={cn(th, "text-right")}>
              <span
                className="group relative inline-flex"
                onKeyDown={(e) => e.key === "Escape" && setHintOff(true)}
                onMouseLeave={() => setHintOff(false)}
                onBlur={() => setHintOff(false)}
              >
                <SortButton active={sort === "mem"} onClick={() => setSort("mem")} describedBy={hintId}>
                  RAM
                </SortButton>
                <span
                  id={hintId}
                  role="tooltip"
                  className={cn(
                    "pointer-events-none absolute top-full right-0 z-20 mt-1.5 hidden w-64 border border-line-strong bg-surface-2 px-2.5 py-2",
                    "text-left font-sans text-[11px] font-normal normal-case tracking-normal text-fg-muted",
                    "shadow-[0_8px_24px_-8px_rgba(0,0,0,0.8)]",
                    !hintOff && "group-hover:block group-focus-within:block",
                  )}
                >
                  {RAM_HINT}
                </span>
              </span>
            </th>
            <th scope="col" className={cn(th, "text-right")}>
              Processos
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((o) => (
            <OwnerRow
              key={o.key}
              owner={o}
              memTotal={memTotal}
              expanded={open.has(o.key)}
              onToggle={() => toggle(o.key)}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}
