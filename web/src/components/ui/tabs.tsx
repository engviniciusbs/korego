"use client";

import { useRef, type KeyboardEvent } from "react";
import { cn } from "@/lib/cn";
import { focusRing } from "./focus";

export interface TabItem<V extends string> {
  value: V;
  label: string;
}

export interface TabsProps<V extends string> {
  /** Base dos ids: as abas viram `${id}-tab-${value}` e o painel `${id}-panel`. */
  id: string;
  /** Nome acessível da lista de abas. */
  label: string;
  items: TabItem<V>[];
  value: V;
  onChange: (value: V) => void;
  className?: string;
}

export const tabId = (id: string, value: string) => `${id}-tab-${value}`;
export const tabPanelId = (id: string) => `${id}-panel`;

/**
 * Abas acessíveis (padrão WAI-ARIA, ativação automática): só a aba ativa entra
 * na ordem do Tab; setas, Home e End movem a seleção. O consumidor renderiza o
 * `role="tabpanel"` com `id={tabPanelId(id)}` e `aria-labelledby={tabId(id, value)}`.
 */
export function Tabs<V extends string>({ id, label, items, value, onChange, className }: TabsProps<V>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const i = items.findIndex((t) => t.value === value);
    let next = -1;
    if (e.key === "ArrowRight") next = (i + 1) % items.length;
    else if (e.key === "ArrowLeft") next = (i - 1 + items.length) % items.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = items.length - 1;
    if (next < 0) return;
    e.preventDefault();
    onChange(items[next].value);
    refs.current[next]?.focus();
  };

  return (
    <div
      role="tablist"
      aria-label={label}
      aria-orientation="horizontal"
      onKeyDown={onKeyDown}
      className={cn("inline-flex border border-line-strong bg-surface", className)}
    >
      {items.map((t, i) => {
        const selected = t.value === value;
        return (
          <button
            key={t.value}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="tab"
            id={tabId(id, t.value)}
            aria-selected={selected}
            aria-controls={tabPanelId(id)}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(t.value)}
            className={cn(
              "h-7 min-w-11 touch-manipulation border-l border-line-strong px-3 font-mono text-xs tabular-nums first:border-l-0",
              "motion-safe:transition-colors motion-safe:duration-100",
              focusRing,
              selected
                ? "bg-accent text-accent-fg"
                : "text-fg-muted hover:bg-surface-2 hover:text-fg",
            )}
          >
            {t.label}
          </button>
        );
      })}
    </div>
  );
}
