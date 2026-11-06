"use client";

import { useEffect, useState, type ReactNode } from "react";
import { FullScreenLoading, useAuthRedirect } from "@/components/shell/auth-redirect";
import { Sidebar } from "@/components/shell/sidebar";
import { Topbar } from "@/components/shell/topbar";
import { focusRing } from "@/components/ui/focus";
import { useAuth } from "@/lib/auth-context";
import { cn } from "@/lib/cn";

export default function PainelLayout({ children }: { children: ReactNode }) {
  const { status } = useAuth();
  const blocked = useAuthRedirect(status, { setup: "/setup/", anonymous: "/login/" });
  const [navOpen, setNavOpen] = useState(false);

  useEffect(() => {
    if (!navOpen) return;
    document.getElementById("navegacao-lateral")?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setNavOpen(false);
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [navOpen]);

  if (blocked) return <FullScreenLoading />;

  return (
    <div className="flex min-h-full flex-1">
      <a
        href="#conteudo"
        className={cn(
          "sr-only z-50 bg-accent px-3 py-2 text-sm text-accent-fg focus:not-sr-only focus:fixed focus:top-2 focus:left-2",
          focusRing,
        )}
      >
        Pular para o conteúdo
      </a>

      {navOpen && (
        <div
          aria-hidden="true"
          className="fixed inset-0 z-30 bg-black/70 lg:hidden"
          onClick={() => setNavOpen(false)}
        />
      )}
      <aside
        id="navegacao-lateral"
        tabIndex={-1}
        className={cn(
          "overscroll-contain outline-none",
          "fixed inset-y-0 left-0 z-40 w-60 border-r border-line bg-surface lg:sticky lg:top-0 lg:z-auto lg:block lg:h-dvh lg:w-56 lg:shrink-0",
          navOpen ? "block" : "hidden",
        )}
      >
        <Sidebar onNavigate={() => setNavOpen(false)} />
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar navOpen={navOpen} onOpenNav={() => setNavOpen(true)} />
        <main id="conteudo" tabIndex={-1} className="flex-1 px-4 py-6 outline-none lg:px-8 lg:py-8">
          <div className="mx-auto w-full max-w-6xl">{children}</div>
        </main>
      </div>
    </div>
  );
}
