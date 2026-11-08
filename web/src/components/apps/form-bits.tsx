import type { ReactNode } from "react";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";

export const selectCls = cn(
  "block h-9 w-full min-w-0 border border-line-strong bg-surface px-3 text-[13px] text-fg",
  "outline-hidden hover:border-fg-subtle focus-visible:border-accent-text focus-visible:bg-surface-2",
  "focus-visible:shadow-[inset_2px_0_0_0_var(--color-accent-text)]",
  "aria-invalid:border-danger disabled:cursor-not-allowed disabled:opacity-50",
);

export const linkCls = cn("text-accent-text underline-offset-2 hover:underline", focusRing);

export function FieldError({ id, children }: { id: string; children?: ReactNode }) {
  if (!children) return null;
  return (
    <p id={id} className="mt-1 text-xs text-danger [overflow-wrap:anywhere]">
      {children}
    </p>
  );
}

export function Hint({ id, children }: { id?: string; children: ReactNode }) {
  return (
    <p id={id} className="mt-1 text-xs text-fg-subtle">
      {children}
    </p>
  );
}

/** Checkbox quadrado do kit (sem cantos arredondados), com o rótulo clicável. */
export function Checkbox({
  checked,
  onChange,
  children,
  describedBy,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  children: ReactNode;
  describedBy?: string;
}) {
  return (
    <label className="flex min-h-9 cursor-pointer items-center gap-2.5 text-[13px] text-fg">
      <span className="relative inline-flex size-4 shrink-0">
        <input
          type="checkbox"
          checked={checked}
          aria-describedby={describedBy}
          onChange={(e) => onChange(e.target.checked)}
          className={cn(
            "peer size-4 cursor-pointer appearance-none border border-line-strong bg-surface",
            "checked:border-accent checked:bg-accent",
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
      {children}
    </label>
  );
}
