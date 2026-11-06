"use client";

import { useId, useState } from "react";
import { Alert, Button, Dialog } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { canRemove, useContainerAction, type Container, type ContainerActionName } from "@/lib/containers";

const REMOVE_HINT = "Pare o container para removê-lo";

const errorMessages: Record<string, string> = {
  CONTAINER_RUNNING: "Pare o container para removê-lo.",
  CONTAINER_NOT_FOUND: "O container não existe mais.",
  DOCKER_UNAVAILABLE: "Docker indisponível.",
  INVALID_CONTAINER_ID: "ID de container inválido.",
};

/** Mensagem em português pelo `code` da API; senão, a mensagem do próprio erro. */
export function actionErrorMessage(err: unknown): string {
  if (err instanceof ApiError) return errorMessages[err.code] ?? err.message;
  return err instanceof Error ? err.message : "Erro desconhecido.";
}

const copy: Record<
  ContainerActionName,
  { label: string; title: string; body: string; danger: boolean }
> = {
  start: { label: "Iniciar", title: "Iniciar container?", body: "será iniciado.", danger: false },
  stop: {
    label: "Parar",
    title: "Parar container?",
    body: "será parado. O que ele serve fica fora do ar até ser iniciado de novo.",
    danger: true,
  },
  restart: {
    label: "Reiniciar",
    title: "Reiniciar container?",
    body: "será parado e iniciado de novo. Há uma breve interrupção.",
    danger: false,
  },
  remove: {
    label: "Remover",
    title: "Remover container?",
    body: "será removido. Esta ação não pode ser desfeita.",
    danger: true,
  },
};

export interface ContainerActionsProps {
  container: Container;
  /** Chamado após uma ação concluída com sucesso. */
  onDone?: (action: ContainerActionName) => void;
}

/**
 * Botões de ação de um container. Toda ação pede confirmação; nos externos o
 * diálogo avisa que o container não foi criado pelo KoreGO.
 */
export function ContainerActions({ container, onDone }: ContainerActionsProps) {
  const mutation = useContainerAction();
  const [pending, setPending] = useState<ContainerActionName | null>(null);
  const hintId = useId();
  const stopped = canRemove(container);
  const busy = mutation.isPending;

  const open = (action: ContainerActionName) => {
    mutation.reset();
    setPending(action);
  };

  const confirm = () => {
    if (!pending) return;
    const action = pending;
    mutation.mutate(
      { id: container.id, action },
      {
        onSuccess: () => {
          setPending(null);
          onDone?.(action);
        },
      },
    );
  };

  const c = pending ? copy[pending] : null;

  return (
    <div className="flex items-center justify-end gap-1.5">
      {/* Mesma chave para Iniciar/Parar: o nó do botão se mantém e o foco não se perde quando o estado muda. */}
      <Button
        key="toggle"
        size="sm"
        variant="secondary"
        disabled={busy}
        onClick={() => open(stopped ? "start" : "stop")}
      >
        {stopped ? "Iniciar" : "Parar"}
      </Button>
      {!stopped && (
        <Button size="sm" variant="secondary" disabled={busy} onClick={() => open("restart")}>
          Reiniciar
        </Button>
      )}
      <Button
        size="sm"
        variant="ghost"
        className="enabled:hover:bg-danger-bg enabled:hover:text-danger"
        disabled={busy || !stopped}
        title={stopped ? undefined : REMOVE_HINT}
        aria-describedby={stopped ? undefined : hintId}
        onClick={() => open("remove")}
      >
        Remover
      </Button>
      {!stopped && (
        <span id={hintId} hidden>
          {REMOVE_HINT}
        </span>
      )}

      <Dialog
        open={c !== null}
        title={c?.title ?? ""}
        confirmLabel={c?.label ?? ""}
        variant={c?.danger ? "danger" : undefined}
        loading={busy}
        onConfirm={confirm}
        onCancel={() => setPending(null)}
      >
        <p>
          O container{" "}
          <strong className="font-mono font-medium text-fg [overflow-wrap:anywhere]" translate="no">
            {container.name}
          </strong>{" "}
          {c?.body}
        </p>
        {!container.managed && (
          <Alert variant="warning" role="note" title="Este container não foi criado pelo KoreGO">
            Ele pode ser gerenciado por outra ferramenta, que talvez desfaça esta ação.
          </Alert>
        )}
        {mutation.isError && (
          <Alert variant="danger" title="A ação falhou.">
            {actionErrorMessage(mutation.error)}
          </Alert>
        )}
      </Dialog>
    </div>
  );
}
