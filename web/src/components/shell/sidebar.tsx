"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Brand } from "@/components/shell/auth-frame";
import { Badge } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";

interface NavItem {
  label: string;
  href?: string;
  /** Etapa em que o item será liberado; presente = desabilitado. */
  stage?: string;
  /** Prefixos de rota que marcam o item como ativo. */
  match?: string | string[];
  children?: Array<{ label: string; href: string }>;
}

const items: NavItem[] = [
  { label: "Visão geral", href: "/" },
  { label: "Consumo", href: "/consumo/", match: "/consumo/" },
  { label: "Containers", href: "/containers/", match: "/containers/" },
  { label: "Projetos", href: "/projetos/", match: ["/projetos/", "/aplicacoes/"] },
  { label: "Domínios", href: "/dominios/", match: "/dominios/" },
  {
    label: "Configurações",
    href: "/configuracoes/seguranca/",
    match: "/configuracoes/",
    children: [
      { label: "Segurança", href: "/configuracoes/seguranca/" },
      { label: "Conexões", href: "/configuracoes/conexoes/" },
      { label: "Atualização", href: "/configuracoes/atualizacao/" },
      { label: "Painel", href: "/configuracoes/painel/" },
      { label: "GitHub", href: "/configuracoes/github/" },
    ],
  },
];

const withSlash = (p: string) => (p.endsWith("/") ? p : `${p}/`);

const linkBase = cn(
  "flex h-8 items-center gap-2 px-3 text-[13px] motion-safe:transition-colors motion-safe:duration-100",
  focusRing,
  "focus-visible:outline-offset-[-2px]",
);

export function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = withSlash(usePathname() ?? "/");

  return (
    <div className="flex h-full flex-col">
      <div className="flex h-12 shrink-0 items-center border-b border-line px-4">
        <Link href="/" onClick={onNavigate} className={cn("text-[15px]", focusRing)}>
          <Brand />
          <span className="sr-only">: visão geral</span>
        </Link>
      </div>
      <nav aria-label="Principal" className="flex-1 overflow-y-auto py-3">
        <ul className="space-y-px">
          {items.map((item) => {
            if (item.stage || !item.href) {
              return (
                <li key={item.label}>
                  <span
                    className="flex h-8 cursor-not-allowed items-center justify-between gap-2 px-3 text-[13px] text-fg-subtle"
                  >
                    <span>
                      {item.label}
                      <span className="sr-only"> (indisponível, chega na {item.stage})</span>
                    </span>
                    <Badge size="sm" aria-hidden="true" className="opacity-80">
                      {item.stage}
                    </Badge>
                  </span>
                </li>
              );
            }
            const prefixes = item.match === undefined ? [] : ([] as string[]).concat(item.match);
            const active = prefixes.length ? prefixes.some((m) => pathname.startsWith(m)) : pathname === item.href;
            return (
              <li key={item.label}>
                <Link
                  href={item.href}
                  onClick={onNavigate}
                  aria-current={active && !item.children ? "page" : undefined}
                  className={cn(
                    linkBase,
                    active
                      ? "bg-surface-2 text-fg shadow-[inset_2px_0_0_0_var(--color-accent)]"
                      : "text-fg-muted hover:bg-surface-2 hover:text-fg",
                  )}
                >
                  {item.label}
                </Link>
                {item.children && active && (
                  <ul className="mt-px space-y-px">
                    {item.children.map((c) => {
                      const childActive = pathname === c.href;
                      return (
                        <li key={c.href}>
                          <Link
                            href={c.href}
                            onClick={onNavigate}
                            aria-current={childActive ? "page" : undefined}
                            className={cn(
                              linkBase,
                              "h-7 pl-6 text-xs",
                              childActive
                                ? "text-accent-text"
                                : "text-fg-muted hover:bg-surface-2 hover:text-fg",
                            )}
                          >
                            <span aria-hidden="true" className="text-fg-subtle">
                              └
                            </span>
                            {c.label}
                          </Link>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </li>
            );
          })}
        </ul>
      </nav>
      <p className="border-t border-line px-4 py-3 font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">
        Etapa 0 · Fundação
      </p>
    </div>
  );
}
