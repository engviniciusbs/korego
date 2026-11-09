"use client";

import Link from "next/link";
import { useId, useRef, useState } from "react";
import { Alert, Button, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import {
  MIGRATING_TITLE,
  processUnit,
  trialFailure,
  trialResult,
  useProxyStatus,
  useRetryProxy,
  useTrialCheck,
  type ProxyStatus,
  type TrialSiteCheck,
} from "@/lib/domains";
import { formatDateTime } from "@/lib/format";
import { MigrationResult, TakeoverDialog, type MigrationOp } from "./takeover-dialog";

export { MIGRATING_TITLE };

/** Título do aviso por estado; `active` não mostra aviso. */
export function proxyBannerTitle(s: ProxyStatus): string {
  switch (s.state) {
    case "blocked":
      return `Proxy inativo: as portas 80/443 estão em uso por ${s.process || "outro processo"}`;
    case "docker_unavailable":
      return "Docker indisponível";
    case "starting":
      return "Proxy iniciando…";
    case "trial":
      return `Ensaio: o Caddy roda em portas de teste enquanto ${s.process || "outro processo"} ocupa 80/443`;
    default:
      return "Proxy com erro";
  }
}

const link = cn("text-xs text-accent-text hover:text-fg hover:underline", focusRing);

function GuideLink({ href }: { href: string }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className={link}>
      Guia de migração <span aria-hidden="true">↗</span>
      <span className="sr-only"> (abre em nova aba)</span>
    </a>
  );
}

const RESULT_TEXT = { ok: "ok", pending: "certificado será emitido após assumir", failed: "" } as const;

/** Uma linha do checklist do ensaio: LED, host, resultado e os caminhos conferidos. */
function TrialItem({ site }: { site: TrialSiteCheck }) {
  const r = trialResult(site);
  const paths = site.paths ?? [];
  return (
    <li data-result={r} className="grid grid-cols-[7px_1fr] items-baseline gap-x-2.5 py-1.5">
      <span
        aria-hidden="true"
        className={cn(
          "size-[7px] translate-y-[-1px]",
          r === "ok" && "bg-success",
          r === "pending" && "bg-warning",
          r === "failed" && "bg-danger",
        )}
      />
      <div className="min-w-0">
        <p className="flex flex-wrap items-baseline gap-x-2">
          <span data-host translate="no" className="font-mono text-[13px] text-fg [overflow-wrap:anywhere]">
            {site.host}
          </span>
          <span
            className={cn(
              "text-xs",
              r === "ok" && "text-success",
              r === "pending" && "text-warning",
              r === "failed" && "text-danger",
            )}
          >
            {r === "failed" ? trialFailure(site) : RESULT_TEXT[r]}
          </span>
        </p>
        {paths.length > 0 && (
          <p className="mt-0.5 flex flex-wrap gap-x-3 font-mono text-[11px] text-fg-subtle" translate="no">
            {paths.map((p) => (
              <span key={p.path} className={cn(!p.ok && "text-danger")}>
                {p.path} {p.status ? p.status : p.error ? "erro" : "—"}
                {p.static && p.index ? " index.html" : ""}
              </span>
            ))}
          </p>
        )}
      </div>
    </li>
  );
}

/** Checklist do ensaio e o botão "Assumir as portas". */
function TrialPanel({ data, onTakeover }: { data: ProxyStatus; onTakeover: () => void }) {
  const check = useTrialCheck(true);
  const reasonId = useId();
  const sites = check.data ?? [];
  const failing = sites.filter((s) => trialResult(s) === "failed").length;

  let reason = "";
  if (data.migrating) reason = `${MIGRATING_TITLE}.`;
  else if (check.isPending) reason = "Conferindo os domínios…";
  else if (check.isError) reason = `Não foi possível conferir os domínios: ${check.error.message}`;
  else if (failing > 0)
    reason = `Corrija os domínios com falha (${failing}) para assumir as portas.`;

  return (
    <div className="mt-1 space-y-2">
      <p>
        Os sites continuam no <span translate="no">{processUnit(data.process)}</span>. Confira cada domínio antes de
        assumir as portas.
      </p>
      {check.isPending ? (
        <p className="flex items-center gap-2 text-xs">
          <Spinner size="sm" decorative />
          <span role="status">Conferindo os domínios no ensaio…</span>
        </p>
      ) : check.isSuccess && sites.length === 0 ? (
        <p className="text-xs">Nenhum domínio cadastrado.</p>
      ) : (
        check.isSuccess && (
          <ul aria-label="Ensaio por domínio" className="divide-y divide-line border-y border-line">
            {sites.map((s) => (
              <TrialItem key={s.host} site={s} />
            ))}
          </ul>
        )
      )}
      <div className="flex flex-wrap items-center gap-3 pt-1">
        <Button
          size="sm"
          variant="primary"
          disabled={reason !== ""}
          aria-describedby={reason ? reasonId : undefined}
          onClick={onTakeover}
        >
          Assumir as portas
        </Button>
        <Button size="sm" loading={check.isFetching && !check.isPending} onClick={() => void check.refetch()}>
          Conferir de novo
        </Button>
        {data.guideUrl && <GuideLink href={data.guideUrl} />}
      </div>
      {reason && (
        <p id={reasonId} className={cn("text-xs", failing > 0 || check.isError ? "text-danger" : "text-fg-subtle")}>
          {reason}
        </p>
      )}
    </div>
  );
}

/**
 * Aviso do estado do proxy (Caddy), para todo estado diferente de `active`.
 * Enquanto o proxy não está ativo, os domínios ficam salvos mas não são servidos.
 *
 * `detailed` (página Domínios): no ensaio mostra o checklist por domínio e
 * "Assumir as portas"; depois de migrado, "Migrado do <unit>" e "Voltar".
 */
export function ProxyBanner({ className, detailed = false }: { className?: string; detailed?: boolean }) {
  const { data } = useProxyStatus();
  const retry = useRetryProxy();
  const [dialog, setDialog] = useState<{ op: MigrationOp; unit: string } | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  if (!data) return null;

  // O botão que abriu o diálogo pode ter sumido (ensaio → migrado): o foco
  // volta para o aviso em vez de cair no <body>.
  const closeDialog = () => {
    setDialog(null);
    setTimeout(() => {
      const active = document.activeElement;
      if (!active || active === document.body) boxRef.current?.focus();
    }, 0);
  };

  const dialogEl = dialog && (
    <TakeoverDialog op={dialog.op} unit={dialog.unit} guideUrl={data.guideUrl} onClose={closeDialog} />
  );

  const last = data.lastMigration;
  const lastUnit = data.migratedFrom || processUnit(data.process);
  const lastResult = detailed && !data.migrating && last && last.outcome !== "ok" && (
    <MigrationResult
      op={last.op}
      outcome={last.outcome}
      message={last.message}
      unit={lastUnit}
      guideUrl={data.guideUrl}
      when={formatDateTime(last.at)}
    />
  );

  let main = null;
  if (data.state === "trial") {
    main = (
      <Alert
        variant="info"
        title={data.migrating ? MIGRATING_TITLE : proxyBannerTitle(data)}
        data-state={data.state}
      >
        {data.migrating && <p>{proxyBannerTitle(data)}.</p>}
        {detailed ? (
          <TrialPanel
            data={data}
            onTakeover={() => setDialog({ op: "takeover", unit: processUnit(data.process) })}
          />
        ) : (
          <p>
            <Link href="/dominios/" className={link}>
              Conferir os domínios e assumir as portas
            </Link>
          </p>
        )}
      </Alert>
    );
  } else if (data.migrating) {
    main = (
      <Alert variant="info" title={MIGRATING_TITLE} data-state="migrating">
        <p>Adicionar, editar e remover domínios fica bloqueado até a troca terminar.</p>
      </Alert>
    );
  } else if (data.state === "active") {
    if (detailed && data.migratedFrom) {
      const unit = data.migratedFrom;
      const when = data.migratedAt ? ` em ${formatDateTime(data.migratedAt)}` : "";
      main = (
        <Alert variant="success" title={`Migrado do ${unit}${when}`} data-state="migrated">
          <p>
            O Caddy atende as portas 80/443. O <span translate="no">{unit}</span> ficou desativado e pode voltar se
            precisar.
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-3">
            <Button
              size="sm"
              disabled={!data.rollbackAvailable}
              onClick={() => setDialog({ op: "rollback", unit })}
            >
              Voltar para o {unit}
            </Button>
            {!data.rollbackAvailable && (
              <span className="text-xs text-fg-subtle">
                A unit <span translate="no">{unit}.service</span> não existe mais neste servidor.
              </span>
            )}
          </div>
        </Alert>
      );
    }
  } else {
    const starting = data.state === "starting";
    main = (
      <Alert variant={starting ? "info" : "warning"} title={proxyBannerTitle(data)} data-state={data.state}>
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
            {data.guideUrl && data.state === "blocked" && <GuideLink href={data.guideUrl} />}
            {retry.isError && <span className="text-xs text-danger">{retry.error.message}</span>}
          </div>
        )}
      </Alert>
    );
  }

  if (!main && !lastResult && !dialogEl) return null;
  return (
    <div ref={boxRef} tabIndex={-1} className={cn("space-y-2 outline-hidden", className)}>
      {main}
      {lastResult}
      {dialogEl}
    </div>
  );
}
