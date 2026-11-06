"use client";

import { memo, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Alert, Badge, Button, Input, Label, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { api } from "@/lib/api";
import { cn } from "@/lib/cn";
import type { ContainerLogsResponse } from "@/lib/containers";
import { LogBuffer, stripAnsi, type LogLine } from "@/lib/logs";
import { useLiveStream } from "@/lib/ws";

const TAIL = 500;
/** Distância do fim (px) abaixo da qual a lista acompanha as linhas novas. */
const BOTTOM_SLACK = 24;

const streamErrors: Record<string, string> = {
  CONTAINER_NOT_FOUND: "O container não existe mais. Volte para a lista de containers.",
  INVALID_CONTAINER_ID: "ID de container inválido.",
};

const timeFormat = new Intl.DateTimeFormat("pt-BR", {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
});

/** `ts` são nanossegundos Unix em texto de 19 dígitos. */
function tsDate(ts: string): Date {
  return new Date(Number(ts.slice(0, -6)));
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

const clean = (lines: LogLine[]): LogLine[] => lines.map((l) => ({ ...l, text: stripAnsi(l.text) }));

const LogRow = memo(function LogRow({ line, re }: { line: LogLine; re: RegExp | null }) {
  const err = line.stream === "stderr";
  const d = tsDate(line.ts);
  const valid = !Number.isNaN(d.getTime());
  // split com grupo de captura: os índices ímpares são as ocorrências.
  const parts = re ? line.text.split(re) : [line.text];
  return (
    <div
      data-stream={line.stream}
      className={cn(
        "grid grid-cols-[4.75rem_minmax(0,1fr)] gap-x-3 px-3 hover:bg-surface-2/70",
        "[contain-intrinsic-size:auto_1.25rem] [content-visibility:auto]",
        err && "bg-warning-bg/40 shadow-[inset_2px_0_0_0_var(--color-warning)]",
      )}
    >
      <span className="select-none whitespace-nowrap text-fg-subtle tabular-nums">
        {valid && <time dateTime={d.toISOString()}>{timeFormat.format(d)}</time>}
        {err && <span className="ml-1.5 text-warning">err</span>}
      </span>
      <span data-testid="log-text" className={cn("whitespace-pre-wrap [overflow-wrap:anywhere]", err ? "text-warning" : "text-fg")}>
        {parts.map((p, i) =>
          i % 2 === 1 ? (
            <mark key={i} className="bg-accent/45 text-fg shadow-[inset_0_-1px_0_0_var(--color-accent-text)]">
              {p}
            </mark>
          ) : (
            p
          ),
        )}
      </span>
    </div>
  );
});

export interface LogViewerProps {
  containerId: string;
  containerName: string;
  /** Falso enquanto a aba está escondida: ao voltar, retoma a rolagem até o fim. */
  active?: boolean;
}

type TailState = { status: "loading" } | { status: "ready" } | { status: "error"; message: string };

/**
 * Logs ao vivo. Junção sem duplicar: assina `container-logs:<id>` primeiro e
 * guarda o que chegar, busca o tail pela REST, `setTail` e `append` do que foi
 * guardado; depois disso as linhas ao vivo vão direto para o buffer.
 */
export function LogViewer({ containerId, containerName, active = true }: LogViewerProps) {
  const buffer = useRef(new LogBuffer());
  // Linhas recebidas antes do tail; null depois da junção.
  const pending = useRef<LogLine[] | null>([]);
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);

  const [lines, setLines] = useState<LogLine[]>([]);
  const [frozen, setFrozen] = useState<LogLine[] | null>(null);
  const [tail, setTail] = useState<TailState>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [ended, setEnded] = useState(false);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const searchId = useId();
  const outputId = useId();

  // O buffer é mutável: o estado recebe sempre uma cópia.
  const publish = useCallback(() => setLines(buffer.current.lines.slice()), []);

  useLiveStream(`container-logs:${containerId}`, (d) => {
    const msg = d as { lines?: LogLine[]; ended?: boolean; error?: string };
    if (msg.error) {
      setStreamError(streamErrors[msg.error] ?? msg.error);
      return;
    }
    if (msg.ended) {
      setEnded(true);
      return;
    }
    if (!msg.lines?.length) return;
    setEnded(false);
    const incoming = clean(msg.lines);
    if (pending.current) {
      pending.current.push(...incoming);
      if (pending.current.length > 5000) pending.current = pending.current.slice(-5000);
      return;
    }
    buffer.current.append(incoming);
    publish();
  });

  // Declarado depois do useLiveStream: a assinatura acontece antes da busca.
  useEffect(() => {
    let cancelled = false;
    api<ContainerLogsResponse>(`/api/containers/${containerId}/logs?tail=${TAIL}`)
      .then((r) => {
        if (cancelled) return;
        buffer.current.setTail(clean(r.lines ?? []));
        buffer.current.append(pending.current ?? []);
        pending.current = null;
        publish();
        setTail({ status: "ready" });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setTail({ status: "error", message: err instanceof Error ? err.message : "Erro desconhecido." });
      });
    return () => {
      cancelled = true;
    };
  }, [containerId, attempt, publish]);

  const paused = frozen !== null;
  const view = frozen ?? lines;
  const newCount = useMemo(() => {
    if (!frozen) return 0;
    const last = frozen.at(-1)?.ts ?? "";
    return lines.filter((l) => l.ts > last).length;
  }, [frozen, lines]);

  const q = query.trim();
  const re = useMemo(() => (q ? new RegExp(`(${escapeRe(q)})`, "gi") : null), [q]);
  const visible = useMemo(() => {
    if (!q) return view;
    const lower = q.toLowerCase();
    return view.filter((l) => l.text.toLowerCase().includes(lower));
  }, [view, q]);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el || paused || !active || !atBottom.current) return;
    el.scrollTop = el.scrollHeight;
  }, [visible, paused, active]);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight <= BOTTOM_SLACK;
  };

  const togglePause = () => {
    if (paused) {
      atBottom.current = true;
      setFrozen(null);
    } else {
      setFrozen(lines);
    }
  };

  const retryTail = () => {
    setTail({ status: "loading" });
    setAttempt((n) => n + 1);
  };

  const total = view.length;
  const live = tail.status === "ready" && !streamError;

  return (
    <section aria-label={`Logs de ${containerName}`} className="border border-line bg-surface">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line px-3 py-2">
        <div className="w-72 max-w-full">
          <Label htmlFor={searchId} className="sr-only">
            Buscar nos logs
          </Label>
          <Input
            id={searchId}
            type="search"
            name="busca-logs"
            size="sm"
            mono
            autoComplete="off"
            spellCheck={false}
            placeholder="Buscar nos logs…"
            aria-controls={outputId}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <p className="font-mono text-[11px] tabular-nums text-fg-subtle">
          {q ? `${visible.length} de ${total} linhas` : `${total} ${total === 1 ? "linha" : "linhas"}`}
        </p>

        <div className="ml-auto flex items-center gap-2">
          {paused ? (
            <Badge size="sm" variant="warning" dot>
              Pausado
            </Badge>
          ) : ended ? (
            <Badge size="sm" variant="neutral" dot>
              Parado
            </Badge>
          ) : (
            <Badge size="sm" variant={live ? "success" : "neutral"} dot>
              {live ? "Ao vivo" : "Conectando"}
            </Badge>
          )}
          {paused && newCount > 0 && (
            <span className="font-mono text-[11px] tabular-nums text-warning">
              {newCount === 1 ? "1 nova linha" : `${newCount} novas linhas`}
            </span>
          )}
          <Button size="sm" variant={paused ? "primary" : "secondary"} onClick={togglePause}>
            {paused ? "Retomar" : "Pausar"}
          </Button>
          <a
            href={`/api/containers/${containerId}/logs/download`}
            download
            className={cn(
              "inline-flex h-7 items-center border border-line-strong bg-surface-2 px-2.5 text-xs font-medium tracking-tight text-fg",
              "hover:border-fg-subtle hover:bg-surface-3 motion-safe:transition-colors motion-safe:duration-100",
              focusRing,
            )}
          >
            Baixar
          </a>
        </div>
      </div>

      <div role="status" aria-live="polite">
        {ended && (
          <p className="border-b border-line bg-warning-bg px-3 py-1.5 font-mono text-xs text-warning">
            Container parado; aguardando reinício…
          </p>
        )}
      </div>
      {streamError && (
        <Alert variant="danger" title="O acompanhamento dos logs parou." className="border-x-0 border-t-0">
          {streamError}
        </Alert>
      )}
      {tail.status === "error" && (
        <Alert variant="danger" title="Não foi possível carregar os últimos logs." className="border-x-0 border-t-0">
          <p>{tail.message}</p>
          <Button size="sm" className="mt-2" onClick={retryTail}>
            Tentar novamente
          </Button>
        </Alert>
      )}

      {/* Região rolável focável: setas e PageUp/PageDown funcionam pelo teclado. */}
      <div
        ref={scroller}
        id={outputId}
        role="region"
        aria-label={`Saída de ${containerName}`}
        tabIndex={0}
        onScroll={onScroll}
        translate="no"
        className={cn(
          "h-[min(65vh,40rem)] min-h-72 overflow-y-auto overscroll-contain bg-bg py-1.5 font-mono text-xs leading-5",
          focusRing,
          "focus-visible:outline-offset-[-2px]",
        )}
      >
        {tail.status === "loading" && total === 0 ? (
          <p className="flex items-center gap-2 px-3 py-2 text-fg-muted">
            <Spinner size="sm" decorative />
            Carregando as últimas {TAIL} linhas…
          </p>
        ) : total === 0 ? (
          <p className="px-3 py-2 text-fg-subtle">Nenhuma linha de log ainda.</p>
        ) : visible.length === 0 ? (
          <p className="px-3 py-2 text-fg-subtle">Nenhuma linha com “{q}”.</p>
        ) : (
          visible.map((l) => <LogRow key={l.ts} line={l} re={re} />)
        )}
      </div>
    </section>
  );
}
