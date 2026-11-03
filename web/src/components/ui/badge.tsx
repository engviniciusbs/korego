import type { ComponentPropsWithRef } from "react";
import { cn } from "@/lib/cn";

export type BadgeVariant = "neutral" | "accent" | "success" | "warning" | "danger" | "info";
export type BadgeSize = "sm" | "md";

const variants: Record<BadgeVariant, string> = {
  neutral: "border-line-strong bg-surface-2 text-fg-muted",
  accent: "border-accent/60 bg-accent/15 text-accent-text",
  success: "border-success/40 bg-success-bg text-success",
  warning: "border-warning/40 bg-warning-bg text-warning",
  danger: "border-danger/40 bg-danger-bg text-danger",
  info: "border-info/40 bg-info-bg text-info",
};

const sizes: Record<BadgeSize, string> = {
  sm: "h-5 px-1.5 text-[10px] gap-1.5",
  md: "h-6 px-2 text-[11px] gap-2",
};

export interface BadgeProps extends ComponentPropsWithRef<"span"> {
  variant?: BadgeVariant;
  size?: BadgeSize;
  /** "LED" quadrado à esquerda do texto, indicando estado. */
  dot?: boolean;
}

export function Badge({
  variant = "neutral",
  size = "md",
  dot = false,
  className,
  children,
  ...props
}: BadgeProps) {
  return (
    <span
      data-variant={variant}
      className={cn(
        "inline-flex items-center whitespace-nowrap border font-mono font-medium uppercase tracking-[0.06em]",
        variants[variant],
        sizes[size],
        className,
      )}
      {...props}
    >
      {dot && <span aria-hidden="true" className="size-1.5 shrink-0 bg-current" />}
      {children}
    </span>
  );
}
