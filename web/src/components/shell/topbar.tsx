"use client";

import { useEffect, useId, useRef, useState } from "react";
import { Button } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { useAuth } from "@/lib/auth-context";
import { cn } from "@/lib/cn";
import { useSystemInfo } from "@/lib/system";
import { LiveStatus } from "./live-status";

function UserMenu() {
  const { user, logout } = useAuth();
  const [open, setOpen] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const menuId = useId();
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        trigger.current?.focus();
      }
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const email = user?.email ?? "";
  const initial = email.charAt(0).toUpperCase() || "?";

  return (
    <div ref={root} className="relative">
      <button
        ref={trigger}
        type="button"
        aria-expanded={open}
        aria-controls={menuId}
        onClick={() => setOpen((o) => !o)}
        className={cn(
          "flex h-8 items-center gap-2 border border-line bg-surface px-1.5 text-[13px] text-fg-muted hover:border-line-strong hover:text-fg",
          focusRing,
        )}
      >
        <span aria-hidden="true" className="flex size-5 items-center justify-center bg-accent/20 font-mono text-[11px] text-accent-text">
          {initial}
        </span>
        <span className="hidden max-w-48 truncate sm:inline">{email}</span>
        <span className="sr-only sm:hidden">Conta</span>
        <span aria-hidden="true" className="font-mono text-[10px] text-fg-subtle">
          ▾
        </span>
      </button>
      {open && (
        <div
          id={menuId}
          className="absolute right-0 top-full z-30 mt-1 w-64 border border-line-strong bg-surface-2 shadow-[0_12px_32px_-8px_rgb(0_0_0/0.8)]"
        >
          <div className="border-b border-line px-3 py-2.5">
            <p className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">
              Conectado como
            </p>
            <p className="truncate text-[13px] text-fg" title={email}>
              {email}
            </p>
          </div>
          <div className="p-1.5">
            <Button
              variant="ghost"
              className="w-full justify-start"
              loading={leaving}
              onClick={async () => {
                setLeaving(true);
                try {
                  await logout();
                } catch {
                  // logout() sempre limpa a sessão local; o guard redireciona.
                }
              }}
            >
              Sair
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

export function Topbar({ onOpenNav, navOpen }: { onOpenNav: () => void; navOpen: boolean }) {
  const { data } = useSystemInfo();
  return (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-surface px-3 lg:px-5">
      <Button
        variant="ghost"
        size="sm"
        className="lg:hidden"
        aria-label="Abrir menu"
        aria-expanded={navOpen}
        aria-controls="navegacao-lateral"
        onClick={onOpenNav}
      >
        <span aria-hidden="true" className="font-mono">
          ≡
        </span>
      </Button>
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="hidden font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle sm:inline">
          Servidor
        </span>
        <span translate="no" className="truncate font-mono text-[13px] text-fg" title={data?.hostname}>
          {data?.hostname ?? "—"}
        </span>
      </div>
      <div className="ml-auto flex items-center gap-2">
        <LiveStatus />
        <UserMenu />
      </div>
    </header>
  );
}
