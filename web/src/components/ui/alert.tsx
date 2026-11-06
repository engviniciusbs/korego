import type { ComponentPropsWithRef, ReactNode } from "react";
import { cn } from "@/lib/cn";

export type AlertVariant = "info" | "success" | "warning" | "danger";

const variants: Record<AlertVariant, { box: string; tag: string; label: string }> = {
  info: { box: "border-l-info bg-info-bg", tag: "text-info", label: "Info" },
  success: { box: "border-l-success bg-success-bg", tag: "text-success", label: "Ok" },
  warning: { box: "border-l-warning bg-warning-bg", tag: "text-warning", label: "Atenção" },
  danger: { box: "border-l-danger bg-danger-bg", tag: "text-danger", label: "Erro" },
};

export interface AlertProps extends Omit<ComponentPropsWithRef<"div">, "title"> {
  variant?: AlertVariant;
  title?: ReactNode;
}

/**
 * Mensagem em linha. `danger` e `warning` usam role="alert" (anúncio imediato);
 * os demais usam role="status" (anúncio educado).
 */
export function Alert({ variant = "info", title, className, children, ...props }: AlertProps) {
  const v = variants[variant];
  const assertive = variant === "danger" || variant === "warning";
  return (
    <div
      role={assertive ? "alert" : "status"}
      data-variant={variant}
      className={cn(
        "flex gap-3 border border-l-2 border-line px-3 py-2.5 text-[13px] text-fg",
        v.box,
        className,
      )}
      {...props}
    >
      <span
        className={cn(
          "w-16 shrink-0 pt-px font-mono text-[11px] font-semibold uppercase tracking-[0.08em]",
          v.tag,
        )}
      >
        {v.label}
      </span>
      <div className="min-w-0 space-y-0.5">
        {title && <p className="font-medium text-fg">{title}</p>}
        {children && <div className="text-fg-muted [overflow-wrap:anywhere]">{children}</div>}
      </div>
    </div>
  );
}
