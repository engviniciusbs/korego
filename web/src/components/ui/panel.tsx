import { useId, type ComponentPropsWithRef, type ReactNode } from "react";
import { cn } from "@/lib/cn";

export type PanelVariant = "default" | "accent" | "danger";
export type PanelSize = "sm" | "md";

/** Trilho de 2px à esquerda: a assinatura visual dos painéis. */
const rails: Record<PanelVariant, string> = {
  default: "",
  accent: "shadow-[inset_2px_0_0_0_var(--color-accent)]",
  danger: "shadow-[inset_2px_0_0_0_var(--color-danger)]",
};

const pads: Record<PanelSize, string> = {
  sm: "p-3",
  md: "p-4",
};

export interface PanelProps extends Omit<ComponentPropsWithRef<"section">, "title"> {
  variant?: PanelVariant;
  size?: PanelSize;
  title?: ReactNode;
  description?: ReactNode;
  /** Ações à direita do cabeçalho (botões, badges). */
  actions?: ReactNode;
  /** Nível do título, para manter a hierarquia de cabeçalhos da página. */
  headingLevel?: 2 | 3 | 4;
}

/** Bloco de conteúdo com cabeçalho opcional. O título rotula a seção (aria-labelledby). */
export function Panel({
  variant = "default",
  size = "md",
  title,
  description,
  actions,
  headingLevel = 2,
  className,
  children,
  ...props
}: PanelProps) {
  const titleId = useId();
  const Heading = `h${headingLevel}` as const;
  const hasHeader = Boolean(title || actions);
  return (
    <section
      aria-labelledby={title ? titleId : undefined}
      data-variant={variant}
      className={cn("border border-line bg-surface", rails[variant], className)}
      {...props}
    >
      {hasHeader && (
        <header
          className={cn(
            "flex min-h-11 items-center justify-between gap-3 border-b border-line",
            size === "sm" ? "px-3 py-2" : "px-4 py-2.5",
          )}
        >
          <div className="min-w-0">
            {title && (
              <Heading id={titleId} className="truncate text-sm font-semibold tracking-tight text-fg">
                {title}
              </Heading>
            )}
            {description && <p className="text-xs text-fg-muted">{description}</p>}
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className={pads[size]}>{children}</div>
    </section>
  );
}
