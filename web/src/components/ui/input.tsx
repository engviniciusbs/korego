import type { ComponentPropsWithRef } from "react";
import { cn } from "@/lib/cn";

export type InputSize = "sm" | "md";

const sizes: Record<InputSize, string> = {
  sm: "h-7 px-2 text-xs",
  md: "h-9 px-3 text-[13px]",
};

export interface InputProps extends Omit<ComponentPropsWithRef<"input">, "size"> {
  size?: InputSize;
  /** Marca o campo como inválido (aria-invalid). Ligue a mensagem via aria-describedby. */
  invalid?: boolean;
  /** Fonte monoespaçada, para IPs, portas, caminhos e tokens. */
  mono?: boolean;
}

export function Input({
  size = "md",
  invalid = false,
  mono = false,
  className,
  ...props
}: InputProps) {
  return (
    <input
      data-size={size}
      aria-invalid={invalid || undefined}
      className={cn(
        "block w-full min-w-0 border border-line-strong bg-surface text-fg placeholder:text-fg-subtle",
        "outline-none motion-safe:transition-colors motion-safe:duration-100",
        "hover:border-fg-subtle focus-visible:border-accent-text focus-visible:bg-surface-2",
        "focus-visible:shadow-[inset_2px_0_0_0_var(--color-accent-text)]",
        "aria-invalid:border-danger aria-invalid:focus-visible:shadow-[inset_2px_0_0_0_var(--color-danger)]",
        "disabled:cursor-not-allowed disabled:opacity-50",
        mono && "font-mono",
        sizes[size],
        className,
      )}
      {...props}
    />
  );
}
