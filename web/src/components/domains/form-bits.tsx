import type { ReactNode } from "react";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";

/** Peças de formulário compartilhadas pelas abas do editor de domínio. */

export const selectCls = cn(
  "block h-9 w-full min-w-0 border border-line-strong bg-surface px-3 text-[13px] text-fg",
  "outline-hidden hover:border-fg-subtle focus-visible:border-accent-text focus-visible:bg-surface-2",
  "focus-visible:shadow-[inset_2px_0_0_0_var(--color-accent-text)]",
  "aria-invalid:border-danger disabled:cursor-not-allowed disabled:opacity-50",
);

export function FieldError({ id, children }: { id: string; children?: string }) {
  if (!children) return null;
  return (
    <p id={id} className="mt-1 text-xs text-danger [overflow-wrap:anywhere]">
      {children}
    </p>
  );
}

export function Hint({ id, children, className }: { id?: string; children: ReactNode; className?: string }) {
  return (
    <p id={id} className={cn("mt-1 text-xs text-fg-subtle", className)}>
      {children}
    </p>
  );
}

/** Junta os ids não vazios para aria-describedby. */
export const describedBy = (...ids: (string | false | undefined)[]) => ids.filter(Boolean).join(" ") || undefined;

interface ToggleProps {
  id?: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  children: ReactNode;
  describedBy?: string;
  invalid?: boolean;
  className?: string;
}

/** Caixa de seleção quadrada do painel, com o rótulo à direita. */
export function Checkbox({ id, checked, onChange, children, describedBy: desc, invalid, className }: ToggleProps) {
  return (
    <label className={cn("flex min-h-9 cursor-pointer items-center gap-2.5 text-[13px] text-fg", className)}>
      <span className="relative inline-flex size-4 shrink-0">
        <input
          id={id}
          type="checkbox"
          checked={checked}
          aria-describedby={desc}
          aria-invalid={invalid || undefined}
          onChange={(e) => onChange(e.target.checked)}
          className={cn(
            "peer size-4 cursor-pointer appearance-none border border-line-strong bg-surface",
            "checked:border-accent checked:bg-accent aria-invalid:border-danger",
            focusRing,
          )}
        />
        <svg
          aria-hidden="true"
          viewBox="0 0 16 16"
          className="pointer-events-none absolute inset-0 hidden size-4 text-accent-fg peer-checked:block"
        >
          <path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square" />
        </svg>
      </span>
      <span>{children}</span>
    </label>
  );
}

/**
 * Interruptor liga/desliga (checkbox com role="switch"): trilho quadrado com o
 * bloco à esquerda (desligado) ou à direita (ligado). Espaço alterna.
 */
export function Switch({ id, checked, onChange, children, describedBy: desc, invalid, className }: ToggleProps) {
  return (
    <label className={cn("flex min-h-9 cursor-pointer items-center gap-3 text-[13px] text-fg", className)}>
      <span className="relative inline-flex h-4 w-7 shrink-0">
        <input
          id={id}
          type="checkbox"
          role="switch"
          checked={checked}
          aria-checked={checked}
          aria-describedby={desc}
          aria-invalid={invalid || undefined}
          onChange={(e) => onChange(e.target.checked)}
          className={cn(
            "peer h-4 w-7 cursor-pointer appearance-none border border-line-strong bg-surface-2",
            "checked:border-accent checked:bg-accent/25 aria-invalid:border-danger",
            focusRing,
          )}
        />
        <span
          aria-hidden="true"
          className={cn(
            "pointer-events-none absolute top-[3px] left-[3px] size-2.5 bg-fg-subtle",
            "motion-safe:transition-transform motion-safe:duration-100",
            "peer-checked:translate-x-3 peer-checked:bg-accent",
          )}
        />
      </span>
      <span>{children}</span>
    </label>
  );
}
