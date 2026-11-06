import type { ComponentPropsWithRef } from "react";
import { cn } from "@/lib/cn";

export interface LabelProps extends ComponentPropsWithRef<"label"> {
  /** Exibe o marcador de campo obrigatório. */
  required?: boolean;
}

/** Rótulo de campo. Associe ao controle com `htmlFor` (= id do Input). */
export function Label({ required = false, className, children, ...props }: LabelProps) {
  return (
    <label
      className={cn(
        "inline-flex items-center gap-1 font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted",
        className,
      )}
      {...props}
    >
      {children}
      {required && (
        <>
          <span aria-hidden="true" className="text-accent-text">
            *
          </span>
          <span className="sr-only">(obrigatório)</span>
        </>
      )}
    </label>
  );
}
