"use client";

import Link from "next/link";
import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Dialog, Input, Label, Panel, Spinner } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { useStackAction, useStackInfo, type StackOp } from "@/lib/adopt";
import { appInputOf, useSaveApp, type AppView, type UpdateMode } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { useContainers } from "@/lib/containers";
import { cleanAuthCode, validAuthCode } from "@/lib/validate";
import { CODE_FORMAT_ERROR, CodeField, isCodeError } from "./code-field";
import { Checkbox, FieldError, Hint, linkCls } from "./form-bits";

const OP_DONE: Record<StackOp, string> = {
  start: "Stack iniciada.",
  stop: "Stack parada.",
  restart: "Stack reiniciada.",
};

/** Parar a stack: pede o código 2FA. */
function StopDialog({ app, onClose }: { app: AppView; onClose: (stopped: boolean) => void }) {
  const uid = useId();
  const formId = `${uid}-form`;
  const action = useStackAction();
  const inputRef = useRef<HTMLInputElement>(null);
  const [code, setCode] = useState("");
  const [codeError, setCodeError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (action.isPending) return;
    setFormError(null);
    const clean = cleanAuthCode(code);
    if (!validAuthCode(clean)) {
      setCodeError(clean ? CODE_FORMAT_ERROR : "Informe o código de confirmação.");
      inputRef.current?.focus();
      return;
    }
    setCodeError(null);
    // O código vale uma vez: não fica no campo depois de enviado.
    setCode("");
    action.mutate(
      { appId: app.id, op: "stop", code: clean },
      {
        onSuccess: () => onClose(true),
        onError: (err) => {
          if (isCodeError(err)) setCodeError(err.message);
          else setFormError(err.message);
          inputRef.current?.focus();
        },
      },
    );
  };

  return (
    <Dialog
      open
      variant="danger"
      eyebrow="Stack compose"
      title={`Parar a stack ${app.composeProject}?`}
      confirmLabel="Parar stack"
      formId={formId}
      loading={action.isPending}
      onCancel={() => onClose(false)}
    >
      <form id={formId} noValidate onSubmit={submit} className="space-y-3">
        <p>
          Os containers da stack param (docker compose stop) e o site sai do ar até você iniciar a stack de novo. Nada é
          apagado.
        </p>
        {formError && <Alert variant="danger">{formError}</Alert>}
        <CodeField
          ref={inputRef}
          id={`${uid}-code`}
          value={code}
          onChange={(v) => {
            setCode(v);
            setCodeError(null);
          }}
          error={codeError}
        />
      </form>
    </Dialog>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-fg-muted">{label}</dt>
      <dd className="font-mono text-[12px] text-fg [overflow-wrap:anywhere]" translate="no">
        {children}
      </dd>
    </>
  );
}

/** Cartão "Stack" de uma app compose-local: pasta, arquivos, containers e iniciar/reiniciar/parar. */
export function StackPanel({ app }: { app: AppView }) {
  const info = useStackInfo(app.id);
  const containers = useContainers();
  const action = useStackAction();
  const [stopping, setStopping] = useState(false);
  const [done, setDone] = useState<string | null>(null);
  const list = (containers.data ?? []).filter((c) => c.project === app.composeProject);

  const run = (op: "start" | "restart") => {
    setDone(null);
    action.mutate({ appId: app.id, op }, { onSuccess: () => setDone(OP_DONE[op]) });
  };

  return (
    <Panel
      title="Stack"
      size="sm"
      actions={
        <div className="flex flex-wrap gap-2">
          <Button size="sm" loading={action.isPending && action.variables?.op === "start"} disabled={action.isPending} onClick={() => run("start")}>
            Iniciar
          </Button>
          <Button size="sm" loading={action.isPending && action.variables?.op === "restart"} disabled={action.isPending} onClick={() => run("restart")}>
            Reiniciar
          </Button>
          <Button
            size="sm"
            variant="danger"
            disabled={action.isPending}
            onClick={() => {
              setDone(null);
              setStopping(true);
            }}
          >
            Parar
          </Button>
        </div>
      }
    >
      <div className="space-y-3">
        {action.isError && <Alert variant="danger">{action.error.message}</Alert>}
        {done && (
          <p role="status" className="text-xs text-success">
            {done}
          </p>
        )}
        <dl className="grid gap-x-4 gap-y-1 text-[13px] sm:grid-cols-[auto_minmax(0,1fr)]">
          <Fact label="Projeto compose">{app.composeProject}</Fact>
          {info.data && (
            <>
              <Fact label="Pasta">{info.data.dir}</Fact>
              <Fact label={info.data.files.length === 1 ? "Arquivo" : "Arquivos"}>{info.data.files.join(", ")}</Fact>
            </>
          )}
        </dl>
        {info.isError && <Alert variant="warning">{info.error.message}</Alert>}
        {containers.isPending ? (
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">Carregando os containers…</span>
          </PanelState>
        ) : containers.isError ? (
          <Alert variant="warning">Não foi possível ler os containers: {containers.error.message}</Alert>
        ) : list.length === 0 ? (
          <p className="text-xs text-fg-subtle">Nenhum container da stack foi encontrado.</p>
        ) : (
          <ul aria-label={`Containers de ${app.composeProject}`} className="border border-line">
            {list.map((c) => (
              <li
                key={c.id}
                className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-line px-3 py-2 first:border-t-0"
              >
                <Link
                  href={`/containers/detalhe/?id=${c.id}`}
                  translate="no"
                  className={cn("font-mono text-[13px] text-fg hover:text-accent-text", linkCls)}
                >
                  {c.service || c.name}
                </Link>
                <Badge size="sm" dot variant={c.state === "running" ? "success" : "warning"}>
                  {c.state === "running" ? "Rodando" : c.state}
                </Badge>
              </li>
            ))}
          </ul>
        )}
      </div>
      {stopping && (
        <StopDialog
          app={app}
          onClose={(stopped) => {
            setStopping(false);
            if (stopped) setDone(OP_DONE.stop);
          }}
        />
      )}
    </Panel>
  );
}

/** Configurações de uma app compose-local: o modo de atualização. */
export function UpdateModeForm({ app }: { app: AppView }) {
  const uid = useId();
  const save = useSaveApp();
  const codeRef = useRef<HTMLInputElement>(null);
  const [script, setScript] = useState(app.updateMode === "script");
  const [path, setPath] = useState(app.updateScript);
  const [code, setCode] = useState("");
  const [errors, setErrors] = useState<{ updateScript?: string; code?: string }>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const mode: UpdateMode = script ? "script" : "pull";
  // Escolher o modo script ou mudar o caminho pede o código (o script roda como root).
  const needsCode = script && (app.updateMode !== "script" || app.updateScript !== path.trim());

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (save.isPending) return;
    const next: typeof errors = {};
    const clean = cleanAuthCode(code);
    if (script && !path.trim()) next.updateScript = "Informe o script, relativo à pasta da stack (por exemplo deploy.sh).";
    if (needsCode && !validAuthCode(clean)) next.code = clean ? CODE_FORMAT_ERROR : "Informe o código de confirmação.";
    setErrors(next);
    setFormError(null);
    setSaved(false);
    if (next.updateScript || next.code) {
      document.getElementById(next.updateScript ? `${uid}-script` : `${uid}-code`)?.focus();
      return;
    }
    setCode("");
    save.mutate(
      {
        id: app.id,
        input: appInputOf(app, {
          updateMode: mode,
          updateScript: script ? path.trim() : "",
          ...(needsCode ? { code: clean } : {}),
        }),
      },
      {
        onSuccess: () => setSaved(true),
        onError: (err) => {
          if (isCodeError(err)) {
            setErrors({ code: err.message });
            codeRef.current?.focus();
          } else if (err instanceof ApiError && err.field === "updateScript") {
            setErrors({ updateScript: err.message });
          } else {
            setFormError(err.message);
          }
        },
      },
    );
  };

  return (
    <Panel title="Atualização" size="sm">
      <form noValidate onSubmit={submit} className="space-y-4">
        {formError && <Alert variant="danger">{formError}</Alert>}
        <p className="text-[13px] text-fg-muted">
          “Atualizar” roda docker compose pull e up -d na pasta da stack, ou um script seu.
        </p>
        <div>
          <Checkbox
            checked={script}
            onChange={(v) => {
              setScript(v);
              setSaved(false);
              setErrors({});
            }}
          >
            Atualizar com um script da stack
          </Checkbox>
        </div>
        {script && (
          <div>
            <Label htmlFor={`${uid}-script`} required>
              Script de atualização
            </Label>
            <Input
              id={`${uid}-script`}
              name="updateScript"
              mono
              className="mt-1.5"
              value={path}
              placeholder="deploy.sh…"
              autoComplete="off"
              spellCheck={false}
              autoCapitalize="none"
              invalid={Boolean(errors.updateScript)}
              aria-describedby={errors.updateScript ? `${uid}-script-error` : `${uid}-script-hint`}
              onChange={(e) => {
                setPath(e.target.value);
                setSaved(false);
                setErrors((x) => ({ ...x, updateScript: undefined }));
              }}
            />
            <FieldError id={`${uid}-script-error`}>{errors.updateScript}</FieldError>
            {!errors.updateScript && <Hint id={`${uid}-script-hint`}>Relativo à pasta da stack; roda como root.</Hint>}
          </div>
        )}
        {needsCode && (
          <CodeField
            ref={codeRef}
            id={`${uid}-code`}
            value={code}
            onChange={(v) => {
              setCode(v);
              setErrors((x) => ({ ...x, code: undefined }));
            }}
            error={errors.code}
          />
        )}
        <div className="flex flex-wrap items-center justify-end gap-3 border-t border-line pt-4">
          {saved && (
            <span role="status" className="text-xs text-success">
              Alterações salvas.
            </span>
          )}
          <Button type="submit" variant="primary" loading={save.isPending}>
            Salvar alterações
          </Button>
        </div>
      </form>
    </Panel>
  );
}
