"use client";

import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { Field, describedBy } from "@/components/shell/auth-frame";
import { Alert, Dialog, Input } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import {
  TOPIC_MIGRATION,
  useProxyStatus,
  useStartMigration,
  type MigrationEvent,
  type MigrationOutcome,
} from "@/lib/domains";
import { cleanAuthCode, validAuthCode } from "@/lib/validate";
import { useLiveStream } from "@/lib/ws";

export type MigrationOp = "takeover" | "rollback";

type StepStatus = "running" | "ok" | "failed";

interface Step {
  step: string;
  status: StepStatus;
  message: string;
}

/** Nome de cada passo publicado em `proxy-migration` (internal/migrate). */
function stepLabel(step: string, unit: string): string {
  switch (step) {
    case "trial_check":
      return "Ensaio de todos os domínios";
    case "stop":
      return `Parar o ${unit}`;
    case "bind":
      return "Caddy assume 80/443";
    case "verify":
      return "Conferir cada domínio";
    case "disable":
      return `Desativar o ${unit}`;
    case "record":
      return "Registrar a troca";
    case "revert_trial":
      return "Caddy volta ao ensaio";
    case "revert_start":
      return `Religar o ${unit}`;
    case "revert_active":
    case "active":
      return `Conferir o ${unit} ativo`;
    case "retake":
      return "Caddy reassume 80/443";
    case "trial":
      return "Caddy em ensaio";
    case "enable":
      return `Ativar o ${unit}`;
    case "start":
      return `Iniciar o ${unit}`;
    case "clear":
      return "Apagar o registro da troca";
    case "restore_disable":
      return `Desativar o ${unit} de novo`;
    default:
      return step;
  }
}

const STATUS_TEXT: Record<StepStatus, string> = { running: "em andamento", ok: "concluído", failed: "falhou" };

/** "voltou para o <unit>: <motivo>", mesmo se o servidor mandar só o motivo. */
export function rolledBackText(unit: string, message: string): string {
  return message.startsWith("voltou para o") ? message : `voltou para o ${unit}: ${message}`;
}

/** Resultado de assumir/voltar: sucesso, desfeito ou falha (vermelho, com o guia). */
export function MigrationResult({
  op,
  outcome,
  message,
  unit,
  guideUrl,
  when,
  className,
}: {
  op: MigrationOp;
  outcome: MigrationOutcome;
  message: string;
  unit: string;
  guideUrl?: string;
  /** Data já formatada do resultado gravado. */
  when?: string;
  className?: string;
}) {
  const at = when ? ` em ${when}` : "";
  if (outcome === "ok") {
    return (
      <Alert variant="success" title={op === "takeover" ? `Migrado do ${unit}` : `De volta ao ${unit}`} className={className}>
        {message && <p>{message}</p>}
      </Alert>
    );
  }
  if (outcome === "rolled_back") {
    return (
      <Alert variant="warning" title={`A troca foi desfeita${at}`} className={className}>
        <p>{rolledBackText(unit, message)}</p>
      </Alert>
    );
  }
  return (
    <Alert
      variant="danger"
      title={op === "takeover" ? `Não foi possível assumir as portas${at}` : `Não foi possível voltar para o ${unit}${at}`}
      className={className}
    >
      <p>{message}</p>
      {guideUrl && (
        <a
          href={guideUrl}
          target="_blank"
          rel="noopener noreferrer"
          className={cn("mt-1 inline-block text-xs text-accent-text hover:text-fg hover:underline", focusRing)}
        >
          Guia de migração <span aria-hidden="true">↗</span>
          <span className="sr-only"> (abre em nova aba)</span>
        </a>
      )}
    </Alert>
  );
}

function Steps({ steps, unit }: { steps: Step[]; unit: string }) {
  return (
    <div aria-live="polite" aria-relevant="additions text">
      <ol aria-label="Passos da troca" className="border-l border-line-strong">
        {steps.length === 0 && (
          <li className="relative py-1.5 pl-4 text-xs text-fg-subtle">
            <span aria-hidden="true" className="absolute top-3 -left-[4px] size-[7px] bg-info motion-safe:animate-pulse" />
            Aguardando o servidor…
          </li>
        )}
        {steps.map((s, i) => (
          <li key={s.step} data-status={s.status} className="relative py-1.5 pl-4">
            <span
              aria-hidden="true"
              className={cn(
                "absolute top-3 -left-[4px] size-[7px]",
                s.status === "ok" && "bg-success",
                s.status === "failed" && "bg-danger",
                s.status === "running" && "bg-info motion-safe:animate-pulse",
              )}
            />
            <p className="flex flex-wrap items-baseline gap-x-2 text-[13px] text-fg">
              <span aria-hidden="true" className="font-mono text-[11px] text-fg-subtle tabular-nums">
                {String(i + 1).padStart(2, "0")}
              </span>
              <span>{stepLabel(s.step, unit)}</span>
              <span
                className={cn(
                  "font-mono text-[11px]",
                  s.status === "ok" && "text-success",
                  s.status === "failed" && "text-danger",
                  s.status === "running" && "text-info",
                )}
              >
                {STATUS_TEXT[s.status]}
              </span>
            </p>
            {s.message && (
              <p className="mt-0.5 font-mono text-xs text-fg-muted [overflow-wrap:anywhere]">{s.message}</p>
            )}
          </li>
        ))}
      </ol>
    </div>
  );
}

/** form → sending (POST) → running (passos ao vivo) → done. */
type Phase = "form" | "sending" | "running" | "done";

/**
 * Confirmação de assumir as portas (ou voltar para o servidor anterior) com o
 * código 2FA. Depois do 202, mostra os passos do tópico `proxy-migration` ao
 * vivo e o resultado. Fechar não interrompe a troca no servidor.
 */
export function TakeoverDialog({
  op,
  unit,
  guideUrl,
  onClose,
}: {
  op: MigrationOp;
  unit: string;
  guideUrl?: string;
  onClose: () => void;
}) {
  const ids = useId();
  const codeId = `${ids}-code`;
  const formId = `${ids}-form`;
  const inputRef = useRef<HTMLInputElement>(null);
  const start = useStartMigration(op);
  const proxy = useProxyStatus().data;

  const [phase, setPhase] = useState<Phase>("form");
  const [code, setCode] = useState("");
  const [codeError, setCodeError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [steps, setSteps] = useState<Step[]>([]);
  const [result, setResult] = useState<{ outcome: MigrationOutcome; message: string } | null>(null);
  // Resultado gravado antes de enviar: a releitura só conta se ele mudou.
  const prevAt = useRef<string | undefined>(undefined);

  useLiveStream(TOPIC_MIGRATION, (d) => {
    // Assinado desde a abertura; conta a partir do envio (os primeiros passos
    // podem chegar antes da resposta 202).
    if (phase !== "sending" && phase !== "running") return;
    const e = d as MigrationEvent | null;
    if (!e || (e.op && e.op !== op)) return;
    if (e.done) {
      setResult({ outcome: e.result ?? "failed", message: e.message });
      setPhase("done");
      return;
    }
    if (!e.step || !e.status) return;
    const next: Step = { step: e.step, status: e.status, message: e.message };
    setSteps((list) => {
      const i = list.findIndex((s) => s.step === next.step);
      if (i < 0) return [...list, next];
      const copy = list.slice();
      copy[i] = next;
      return copy;
    });
  });

  // Sem o evento final (WS caiu): a releitura de /api/proxy traz o resultado.
  const last = proxy?.lastMigration;
  useEffect(() => {
    if (phase !== "running" || proxy?.migrating || !last) return;
    if (last.op !== op || last.at === prevAt.current) return;
    setResult({ outcome: last.outcome, message: last.message });
    setPhase("done");
  }, [phase, proxy?.migrating, last, op]);

  function submit(e: FormEvent) {
    e.preventDefault();
    // Enter repetido ou duplo clique: um envio só.
    if (phase !== "form") return;
    setFormError(null);
    const clean = cleanAuthCode(code);
    if (!validAuthCode(clean)) {
      setCodeError("Use os 6 dígitos do autenticador ou um código de recuperação.");
      inputRef.current?.focus();
      return;
    }
    setCodeError(null);
    prevAt.current = proxy?.lastMigration?.at;
    // O código vale uma vez: não fica no campo depois de enviado.
    setCode("");
    setPhase("sending");
    start.mutate(clean, {
      onSuccess: () => setPhase((p) => (p === "sending" ? "running" : p)),
      onError: (err) => {
        setPhase("form");
        if (err instanceof ApiError && ["CODE_REQUIRED", "INVALID_CODE", "RATE_LIMITED"].includes(err.code)) {
          setCodeError(err.message);
        } else {
          setFormError(err.message);
        }
        setCode("");
        inputRef.current?.focus();
      },
    });
  }

  const actionLabel = op === "takeover" ? "Assumir as portas" : `Voltar para o ${unit}`;
  const sending = phase === "sending";

  if (phase === "form" || sending) {
    return (
      <Dialog
        open
        eyebrow="Troca de servidor"
        title={op === "takeover" ? `Assumir as portas do ${unit}?` : `Voltar para o ${unit}?`}
        confirmLabel={actionLabel}
        formId={formId}
        size="lg"
        loading={sending}
        onCancel={onClose}
      >
        <form id={formId} noValidate onSubmit={submit} className="space-y-4">
          {op === "takeover" ? (
            <ol className="list-decimal space-y-1 pl-5 marker:font-mono marker:text-fg-subtle">
              <li>
                Para o {unit}.
              </li>
              <li>O Caddy assume as portas 80/443.</li>
              <li>Cada domínio é conferido; certificados automáticos têm até 60 s para sair.</li>
              <li>
                Se algo falhar, o KoreGO volta automaticamente para o {unit}.
              </li>
            </ol>
          ) : (
            <ol className="list-decimal space-y-1 pl-5 marker:font-mono marker:text-fg-subtle">
              <li>O Caddy volta para o ensaio, nas portas de teste.</li>
              <li>
                O {unit} é ativado e iniciado de novo.
              </li>
              <li>
                O KoreGO confere se o {unit} ficou ativo.
              </li>
            </ol>
          )}
          <p>Enquanto a troca roda, a edição de domínios fica bloqueada. Ela leva até alguns minutos.</p>
          {formError && <Alert variant="danger">{formError}</Alert>}
          <Field
            id={codeId}
            label="Código de confirmação"
            required
            error={codeError ?? undefined}
            hint={
              <>
                6 dígitos do autenticador, ou um código de recuperação no formato{" "}
                <code translate="no" className="font-mono text-fg-muted">
                  xxxxx-xxxxx
                </code>
                .
              </>
            }
          >
            <Input
              ref={inputRef}
              id={codeId}
              name="code"
              mono
              autoComplete="one-time-code"
              spellCheck={false}
              autoCapitalize="none"
              maxLength={11}
              placeholder="000000"
              className="max-w-48 tracking-[0.2em]"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              invalid={Boolean(codeError)}
              aria-describedby={describedBy(codeId, true, codeError ?? undefined)}
            />
          </Field>
        </form>
      </Dialog>
    );
  }

  return (
    <Dialog
      open
      eyebrow="Troca de servidor"
      title={op === "takeover" ? "Assumindo as portas" : `Voltando para o ${unit}`}
      confirmLabel="Fechar"
      hideCancel
      size="lg"
      onConfirm={onClose}
      onCancel={onClose}
    >
      <Steps steps={steps} unit={unit} />
      {phase === "done" && result ? (
        <MigrationResult op={op} outcome={result.outcome} message={result.message} unit={unit} guideUrl={guideUrl} />
      ) : (
        <p className="text-xs text-fg-subtle">Pode fechar: a troca continua no servidor.</p>
      )}
    </Dialog>
  );
}
