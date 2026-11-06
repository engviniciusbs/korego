import type { ReactNode } from "react";

/** Cabeçalho de página do painel: trilha (eyebrow) + h1 + descrição. */
export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow?: string;
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4 border-b border-line pb-4">
      <div className="min-w-0">
        {eyebrow && (
          <p className="font-mono text-[11px] uppercase tracking-[0.12em] text-fg-subtle">{eyebrow}</p>
        )}
        <h1 className="mt-1 text-xl font-semibold tracking-tight text-balance text-fg [overflow-wrap:anywhere]">{title}</h1>
        {description && <p className="mt-1 text-[13px] text-fg-muted">{description}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

/** Estado de carregamento/vazio dentro de um painel. */
export function PanelState({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-24 items-center justify-center gap-3 text-[13px] text-fg-muted">
      {children}
    </div>
  );
}
