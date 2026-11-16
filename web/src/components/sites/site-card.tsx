"use client";

import Link from "next/link";
import { linkCls } from "@/components/apps/form-bits";
import { Badge, Button } from "@/components/ui";
import { cn } from "@/lib/cn";
import { formatDateTime } from "@/lib/format";
import { SITE_KIND_LABEL, rootVersion, siteHref, type DetectedSite } from "@/lib/sites";

/** Como o site é atualizado, em uma linha. */
export function siteOrigin(s: DetectedSite): string {
  switch (s.kind) {
    case "compose":
      return "Atualizado fora do painel (docker compose)";
    case "avulso":
      return "Atualizado fora do painel (container)";
    case "estatico": {
      const root = s.roots[0]?.root;
      return root ? `Atualizado fora do painel (pasta ${root})` : "Atualizado fora do painel";
    }
    case "endereco":
      return "Atualizado fora do painel";
  }
}

/** Resumo do conteúdo, na linha de baixo do cartão. */
function summary(s: DetectedSite): string {
  if (s.kind === "compose" || s.kind === "avulso") {
    const running = s.containers.filter((c) => c.state === "running").length;
    const n = s.containers.length;
    return `${running} de ${n} ${n === 1 ? "container rodando" : "containers rodando"}`;
  }
  if (s.kind === "endereco") return s.domains[0]?.routes.find((r) => r.address)?.address ?? "";
  return `${s.domains.length} ${s.domains.length === 1 ? "domínio" : "domínios"}`;
}

function StaticLine({ s }: { s: DetectedSite }) {
  const r = s.roots[0];
  if (!r) return null;
  if (!r.exists) {
    return <p className="text-[13px] text-warning">Pasta não encontrada: {r.root}</p>;
  }
  const v = rootVersion(r);
  return (
    <p className="text-[13px] text-fg-muted [overflow-wrap:anywhere]">
      Versão <span className="font-mono text-[12px] text-fg" translate="no">{v}</span>
      {r.modifiedAt && <> · publicada em {formatDateTime(r.modifiedAt)}</>}
    </p>
  );
}

/**
 * Cartão de um site da VPS. `onAdopt` abre "Gerenciar pelo KoreGO": o diálogo
 * fica com quem lista os cartões, porque o site sai da lista ao ser adotado.
 */
export function SiteCard({ s, onAdopt }: { s: DetectedSite; onAdopt?: (s: DetectedSite) => void }) {
  const stopped = (s.kind === "compose" || s.kind === "avulso") && s.containers.some((c) => c.state !== "running");
  const missing = s.kind === "estatico" && s.roots.some((r) => !r.exists);
  // Só estáticos com a pasta no lugar e stacks compose podem ser adotados.
  const adoptable = s.kind === "compose" || (s.kind === "estatico" && s.roots.length > 0 && !missing);
  return (
    <li className="group flex min-w-0 flex-col border border-line bg-surface motion-safe:transition-colors hover:border-line-strong">
      <div className="flex-1 space-y-1.5 px-4 pt-3.5 pb-3 shadow-[inset_2px_0_0_0_var(--color-line-strong)] group-hover:shadow-[inset_2px_0_0_0_var(--color-accent)]">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="min-w-0 text-[15px] font-semibold tracking-tight">
            <Link
              href={siteHref(s)}
              translate="no"
              className={cn("text-fg [overflow-wrap:anywhere] hover:text-accent-text", linkCls)}
            >
              {s.name}
            </Link>
          </h2>
          <Badge size="sm" variant="info">
            {SITE_KIND_LABEL[s.kind]}
          </Badge>
          {(stopped || missing) && (
            <Badge size="sm" variant="warning" dot>
              {missing ? "Sem pasta" : "Parado"}
            </Badge>
          )}
        </div>
        {s.kind === "estatico" && <StaticLine s={s} />}
        {s.domains.length > 0 && s.kind !== "estatico" && (
          <p className="font-mono text-[12px] text-fg-muted [overflow-wrap:anywhere]" translate="no">
            {s.domains.map((d) => d.hostname).join(" · ")}
          </p>
        )}
        <p className="text-[12px] text-fg-subtle [overflow-wrap:anywhere]">{siteOrigin(s)}</p>
      </div>
      <div className="flex items-center justify-between gap-3 border-t border-line px-4 py-2">
        <span className="font-mono text-[11px] uppercase tracking-[0.08em] text-fg-muted tabular-nums [overflow-wrap:anywhere]">
          {summary(s)}
        </span>
        {adoptable && onAdopt && (
          <Button size="sm" variant="ghost" onClick={() => onAdopt(s)}>
            Gerenciar pelo KoreGO<span className="sr-only"> ({s.name})</span>
          </Button>
        )}
      </div>
    </li>
  );
}
