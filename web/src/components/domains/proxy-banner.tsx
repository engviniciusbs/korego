"use client";

import { Alert, Button } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import { useProxyStatus, useRetryProxy, type ProxyStatus } from "@/lib/domains";

/** Título do aviso por estado; `active` não mostra aviso. */
export function proxyBannerTitle(s: ProxyStatus): string {
  switch (s.state) {
    case "blocked":
      return `Proxy inativo: as portas 80/443 estão em uso por ${s.process || "outro processo"}`;
    case "docker_unavailable":
      return "Docker indisponível";
    case "starting":
      return "Proxy iniciando…";
    default:
      return "Proxy com erro";
  }
}

/**
 * Aviso do estado do proxy (Caddy), para todo estado diferente de `active`.
 * Enquanto o proxy não está ativo, os domínios ficam salvos mas não são servidos.
 */
export function ProxyBanner({ className }: { className?: string }) {
  const { data } = useProxyStatus();
  const retry = useRetryProxy();
  if (!data || data.state === "active") return null;

  const starting = data.state === "starting";
  return (
    <Alert
      variant={starting ? "info" : "warning"}
      title={proxyBannerTitle(data)}
      className={className}
      data-state={data.state}
    >
      {data.state === "error" && data.message && <p>{data.message}</p>}
      {data.state === "blocked" && (
        <p>Os domínios ficam salvos, mas só passam a responder quando o KoreGO assumir as portas 80 e 443.</p>
      )}
      {data.state === "docker_unavailable" && <p>O proxy roda em um container; inicie o Docker no servidor.</p>}
      {!starting && (
        <div className="mt-2 flex flex-wrap items-center gap-3">
          <Button size="sm" loading={retry.isPending} onClick={() => retry.mutate()}>
            Tentar novamente
          </Button>
          {data.guideUrl && data.state === "blocked" && (
            <a
              href={data.guideUrl}
              target="_blank"
              rel="noopener noreferrer"
              className={cn("text-xs text-accent-text hover:text-fg hover:underline", focusRing)}
            >
              Guia de migração <span aria-hidden="true">↗</span>
              <span className="sr-only"> (abre em nova aba)</span>
            </a>
          )}
          {retry.isError && <span className="text-xs text-danger">{retry.error.message}</span>}
        </div>
      )}
    </Alert>
  );
}
