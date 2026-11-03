import type { ComponentPropsWithRef } from "react";
import { cn } from "@/lib/cn";
import { focusRing } from "./focus";
import { Spinner } from "./spinner";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export type ButtonSize = "sm" | "md" | "lg";

const variants: Record<ButtonVariant, string> = {
  primary:
    "bg-accent text-accent-fg border-accent hover:bg-accent-hover hover:border-accent-hover",
  secondary:
    "bg-surface-2 text-fg border-line-strong hover:bg-surface-3 hover:border-fg-subtle",
  ghost:
    "bg-transparent text-fg-muted border-transparent hover:bg-surface-2 hover:text-fg",
  danger:
    "bg-danger-strong text-white border-danger-strong hover:bg-danger-strong-hover hover:border-danger-strong-hover",
};

const sizes: Record<ButtonSize, string> = {
  sm: "h-7 px-2.5 text-xs gap-1.5",
  md: "h-8 px-3 text-[13px] gap-2",
  lg: "h-10 px-4 text-sm gap-2",
};

export interface ButtonProps extends ComponentPropsWithRef<"button"> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** Mostra o spinner, define aria-busy e desabilita o botão. */
  loading?: boolean;
}

export function Button({
  variant = "secondary",
  size = "md",
  loading = false,
  disabled,
  type = "button",
  className,
  children,
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      data-variant={variant}
      data-size={size}
      disabled={disabled || loading}
      aria-busy={loading ? true : undefined}
      className={cn(
        "inline-flex touch-manipulation select-none items-center justify-center whitespace-nowrap border font-medium tracking-tight",
        "motion-safe:transition-colors motion-safe:duration-100",
        "disabled:cursor-not-allowed disabled:opacity-50",
        focusRing,
        variants[variant],
        sizes[size],
        className,
      )}
      {...props}
    >
      {loading && <Spinner size="sm" decorative />}
      {children}
    </button>
  );
}
