"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { Spinner } from "@/components/ui";
import type { AuthStatus } from "@/lib/auth-context";

/** Para onde cada status deve ir; ausente = pode ficar na página. */
export type RedirectMap = Partial<Record<Exclude<AuthStatus, "loading">, string>>;

/**
 * Redireciona no cliente conforme o status de autenticação.
 * Retorna true enquanto a página não deve exibir conteúdo
 * (status carregando ou redirecionamento em curso).
 */
export function useAuthRedirect(status: AuthStatus, map: RedirectMap, enabled = true): boolean {
  const router = useRouter();
  const target = enabled && status !== "loading" ? map[status] : undefined;
  useEffect(() => {
    if (target) router.replace(target);
  }, [target, router]);
  return status === "loading" || Boolean(target);
}

/** Tela cheia neutra exibida enquanto o status resolve: nunca vaza conteúdo protegido. */
export function FullScreenLoading({ label = "Carregando…" }: { label?: string }) {
  return (
    <div className="flex flex-1 items-center justify-center bg-bg text-fg-muted">
      <div className="flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.12em]">
        <Spinner size="sm" decorative />
        <span role="status">{label}</span>
      </div>
    </div>
  );
}
