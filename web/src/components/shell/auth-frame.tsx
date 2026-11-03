import type { ReactNode } from "react";
import { Label } from "@/components/ui";

export function Brand({ className }: { className?: string }) {
  return (
    <span translate="no" className={`font-mono font-semibold tracking-tight ${className ?? ""}`}>
      Kore<span className="text-accent-text">GO</span>
    </span>
  );
}

/**
 * Moldura das telas sem sessão (setup, login): coluna estreita centralizada
 * sobre uma grade técnica discreta.
 */
export function AuthFrame({
  eyebrow,
  title,
  aside,
  children,
  wide = false,
}: {
  eyebrow: string;
  title: string;
  /** Conteúdo à direita do eyebrow (ex.: indicador de passos). */
  aside?: ReactNode;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <main
      id="conteudo"
      className="relative flex flex-1 flex-col items-center justify-center px-4 py-12 [background-image:linear-gradient(var(--color-line)_1px,transparent_1px),linear-gradient(90deg,var(--color-line)_1px,transparent_1px)] [background-position:-1px_-1px] [background-size:48px_48px]"
    >
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_center,transparent_0%,var(--color-bg)_70%)]"
      />
      <div className={`relative w-full ${wide ? "max-w-xl" : "max-w-md"}`}>
        <div className="mb-6 flex items-end justify-between gap-4">
          <Brand className="text-xl" />
          <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-fg-subtle">
            Painel do servidor
          </span>
        </div>
        <div className="border border-line bg-surface shadow-[inset_2px_0_0_0_var(--color-accent)]">
          <header className="border-b border-line px-6 pt-5 pb-4">
            <div className="flex items-center justify-between gap-3">
              <p className="font-mono text-[11px] uppercase tracking-[0.12em] text-accent-text">
                {eyebrow}
              </p>
              {aside}
            </div>
            <h1 id="auth-title" tabIndex={-1} className="mt-1.5 text-lg outline-none font-semibold tracking-tight text-balance text-fg">
              {title}
            </h1>
          </header>
          <div className="px-6 py-5">{children}</div>
        </div>
      </div>
    </main>
  );
}

/** Campo com rótulo, dica e erro ligados por aria-describedby. */
export function Field({
  id,
  label,
  hint,
  error,
  required,
  children,
  labelFor,
}: {
  id: string;
  label: ReactNode;
  hint?: ReactNode;
  error?: string;
  required?: boolean;
  children: ReactNode;
  labelFor?: string;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={labelFor ?? id} required={required}>
        {label}
      </Label>
      {children}
      {hint && !error && (
        <p id={`${id}-hint`} className="text-xs text-fg-subtle">
          {hint}
        </p>
      )}
      {error && (
        <p id={`${id}-error`} className="text-xs text-danger">
          {error}
        </p>
      )}
    </div>
  );
}

/** aria-describedby para um Field. */
export function describedBy(id: string, hint: boolean, error?: string): string | undefined {
  if (error) return `${id}-error`;
  return hint ? `${id}-hint` : undefined;
}
