"use client";

import { useQueryClient } from "@tanstack/react-query";
import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { shortSha, statusVariant } from "@/components/apps/page-bits";
import { Alert, Badge, Button, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import {
  DeployLogBuffer,
  fetchDeployLog,
  isActive,
  statusLabel,
  useDeploy,
  type Deploy,
  type DeployEvent,
  type DeployLogEntry,
} from "@/lib/deploys";
import { formatDateTime } from "@/lib/format";
import { stripAnsi } from "@/lib/logs";
import { getLiveSocket, useLiveStream } from "@/lib/ws";
import { CancelDeployButton, deployDuration, triggerLabel } from "./deploy-list";

/** Máximo de entradas por resposta do GET /log: resposta cheia pede a próxima página. */
const PAGE = 5000;
/** Distância do fim (px) abaixo da qual o log acompanha as linhas novas. */
const BOTTOM_SLACK = 24;
/** Linhas ao vivo guardadas enquanto uma busca está em andamento. */
const MAX_PENDING = 10000;

const LogRow = memo(function LogRow({ line }: { line: DeployLogEntry }) {
  return (
    <div className="grid grid-cols-[3.5rem_minmax(0,1fr)] gap-x-3 px-3 hover:bg-surface-2/70 [contain-intrinsic-size:auto_1.25rem] [content-visibility:auto]">
      <span aria-hidden="true" className="select-none text-right text-fg-subtle tabular-nums">
        {line.n}
      </span>
      <span data-testid="log-text" className="whitespace-pre-wrap text-fg [overflow-wrap:anywhere]">
        {line.text}
      </span>
    </div>
  );
});

type SyncError = string | null;

/**
 * Junta o histórico (REST) e o tópico `deploy-logs:<id>` por `n`. Assina
 * primeiro e guarda o que chegar enquanto busca; depois da busca as linhas
 * guardadas entram no buffer. Lacuna, reconexão ou "Tentar novamente" refazem
 * a busca a partir do último `n` contíguo.
 */
function useDeployLogStream(id: number) {
  const qc = useQueryClient();
  const [lines, setLines] = useState<DeployLogEntry[]>([]);
  const [done, setDone] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<SyncError>(null);
  const retryRef = useRef<() => void>(() => undefined);

  useEffect(() => {
    const buf = new DeployLogBuffer();
    let cancelled = false;
    // Linhas ao vivo recebidas durante uma busca; null fora de busca.
    let pending: DeployLogEntry[] | null = [];
    let syncing = false;
    let again = false;

    const publish = () => {
      if (cancelled) return;
      setLines([...buf.lines]);
      setDone(buf.done);
    };

    const sync = async (from: number): Promise<void> => {
      pending ??= [];
      if (syncing) {
        again = true;
        return;
      }
      syncing = true;
      setLoading(true);
      let ok = false;
      try {
        const all: DeployLogEntry[] = [];
        let cursor = from;
        let finished = false;
        for (;;) {
          const r = await fetchDeployLog(id, cursor);
          if (cancelled) return;
          const entries = r.entries ?? [];
          all.push(...entries);
          finished = r.done;
          if (finished || entries.length < PAGE) break;
          cursor = entries[entries.length - 1].n;
        }
        buf.setInitial(all);
        const live = pending ?? [];
        pending = null;
        // Refetch sem nada novo (o servidor já descartou o trecho): aceita o
        // ao vivo como está em vez de pedir a mesma lacuna para sempre.
        if (from > 0 && all.length === 0) buf.setInitial(live);
        else buf.append(live);
        if (finished) buf.markDone();
        setError(null);
        ok = true;
      } catch (err) {
        if (cancelled) return;
        // Continua guardando o ao vivo até a próxima tentativa.
        setError(err instanceof Error ? err.message : "Erro desconhecido.");
      } finally {
        syncing = false;
        if (!cancelled) setLoading(false);
      }
      publish();
      if (!ok) return;
      if (again || buf.needsRefetch) {
        again = false;
        void sync(buf.refetchFrom);
      }
    };

    const onLine = (d: unknown) => {
      const m = d as Partial<DeployLogEntry> & { done?: boolean };
      if (m?.done) {
        buf.markDone();
        publish();
        void qc.invalidateQueries({ queryKey: ["deploy", id] });
        return;
      }
      if (typeof m?.n !== "number") return;
      const entry = { n: m.n, text: stripAnsi(m.text ?? "") };
      if (pending) {
        pending.push(entry);
        if (pending.length > MAX_PENDING) pending.splice(0, pending.length - MAX_PENDING);
        return;
      }
      buf.append([entry]);
      if (buf.needsRefetch) void sync(buf.refetchFrom);
      publish();
    };

    const socket = getLiveSocket();
    // Assina antes da primeira busca.
    const unsub = socket.subscribe(`deploy-logs:${id}`, onLine);
    let opened = socket.status === "open";
    let dropped = false;
    const unStatus = socket.onStatus((s) => {
      if (s === "closed") dropped = true;
      if (s !== "open") return;
      // Reconexão: o que chegou durante a queda se perdeu.
      if (opened && dropped && !buf.done) void sync(buf.refetchFrom);
      opened = true;
      dropped = false;
    });
    retryRef.current = () => void sync(buf.refetchFrom);
    void sync(0);

    return () => {
      cancelled = true;
      unsub();
      unStatus();
    };
  }, [id, qc]);

  const retry = useCallback(() => retryRef.current(), []);
  return { lines, done, loading, error, retry };
}

/** Status do deploy: GET /api/deploys/{id} + eventos do tópico `deploys`. */
function useLiveDeploy(id: number) {
  const qc = useQueryClient();
  const deploy = useDeploy(id);
  useLiveStream("deploys", (d) => {
    const ev = d as Partial<DeployEvent> | null;
    if (ev?.id !== id) return;
    qc.setQueryData<Deploy>(["deploy", id], (old) =>
      old ? { ...old, status: ev.status ?? old.status, reason: ev.reason ?? old.reason } : old,
    );
    // Datas e imagem vêm só do REST.
    void qc.invalidateQueries({ queryKey: ["deploy", id] });
  });
  return deploy;
}

function Header({ deploy, done, count }: { deploy: Deploy | undefined; done: boolean; count: number }) {
  const duration = deploy ? deployDuration(deploy) : null;
  return (
    <div className="border-b border-line">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2.5">
        {deploy ? (
          <>
            <span aria-live="polite">
              <Badge dot variant={statusVariant(deploy.status)}>
                {statusLabel(deploy.status)}
              </Badge>
            </span>
            <dl className="flex min-w-0 flex-wrap items-baseline gap-x-4 gap-y-1 font-mono text-[11px] text-fg-muted">
              <div className="flex gap-1.5">
                <dt className="text-fg-subtle">gatilho</dt>
                <dd>{triggerLabel(deploy.trigger)}</dd>
              </div>
              {deploy.commitSha && (
                <div className="flex min-w-0 gap-1.5">
                  <dt className="text-fg-subtle">commit</dt>
                  <dd className="min-w-0 truncate" title={deploy.commitMessage || deploy.commitSha} translate="no">
                    {shortSha(deploy.commitSha)}
                    {deploy.commitMessage && <span className="ml-2 font-sans text-fg-muted">{deploy.commitMessage}</span>}
                  </dd>
                </div>
              )}
              <div className="flex gap-1.5">
                <dt className="text-fg-subtle">início</dt>
                <dd className="tabular-nums">{formatDateTime(deploy.startedAt ?? deploy.createdAt)}</dd>
              </div>
              {duration && (
                <div className="flex gap-1.5">
                  <dt className="text-fg-subtle">duração</dt>
                  <dd className="tabular-nums">{duration}</dd>
                </div>
              )}
            </dl>
          </>
        ) : (
          <Spinner size="sm" label="Carregando o deploy…" />
        )}
        <div className="ml-auto flex items-center gap-3">
          <p className="font-mono text-[11px] tabular-nums text-fg-subtle">
            {count} {count === 1 ? "linha" : "linhas"}
          </p>
          {done ? (
            <Badge size="sm" variant="neutral">
              Log completo
            </Badge>
          ) : (
            <Badge size="sm" variant="info" dot>
              Ao vivo
            </Badge>
          )}
          {deploy && isActive(deploy.status) && <CancelDeployButton deploy={deploy} />}
        </div>
      </div>
      {deploy?.reason && (
        <p
          className={cn(
            "border-t border-line px-3 py-2 text-[13px] [overflow-wrap:anywhere]",
            deploy.status === "failed" ? "bg-danger-bg text-danger" : "text-fg-muted",
          )}
        >
          <span className="mr-2 font-mono text-[10px] uppercase tracking-[0.12em]">Motivo</span>
          {deploy.reason}
        </p>
      )}
    </div>
  );
}

export interface DeployLogProps {
  id: number;
  /** Altura da área do log; o padrão acompanha a janela. */
  className?: string;
}

/** Log do deploy ao vivo, com o status e o motivo no topo. */
export function DeployLog({ id, className }: DeployLogProps) {
  const deploy = useLiveDeploy(id);
  const { lines, done, loading, error, retry } = useDeployLogStream(id);
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el || !atBottom.current) return;
    el.scrollTop = el.scrollHeight;
  }, [lines]);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight <= BOTTOM_SLACK;
  };

  return (
    <section aria-label={`Log do deploy #${id}`} className="border border-line bg-surface">
      <Header deploy={deploy.data} done={done} count={lines.length} />
      {error && (
        <Alert variant="danger" title="Não foi possível carregar o log." className="border-x-0 border-t-0">
          <p>{error}</p>
          <Button size="sm" className="mt-2" loading={loading} onClick={retry}>
            Tentar novamente
          </Button>
        </Alert>
      )}
      {/* Região rolável focável: setas e PageUp/PageDown funcionam pelo teclado. */}
      <div
        ref={scroller}
        role="region"
        aria-label={`Saída do deploy #${id}`}
        tabIndex={0}
        onScroll={onScroll}
        translate="no"
        className={cn(
          "h-[calc(100dvh-16rem)] min-h-80 overflow-y-auto overscroll-contain bg-bg py-1.5 font-mono text-xs leading-5",
          focusRing,
          "focus-visible:outline-offset-[-2px]",
          className,
        )}
      >
        {lines.length === 0 ? (
          loading ? (
            <p className="flex items-center gap-2 px-3 py-2 text-fg-muted">
              <Spinner size="sm" decorative />
              Carregando o log…
            </p>
          ) : (
            <p className="px-3 py-2 text-fg-subtle">{done ? "O deploy não gerou log." : "Aguardando as primeiras linhas…"}</p>
          )
        ) : (
          lines.map((l) => <LogRow key={l.n} line={l} />)
        )}
      </div>
    </section>
  );
}
