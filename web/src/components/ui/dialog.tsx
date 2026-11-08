"use client";

import { useEffect, useId, useRef, type KeyboardEvent, type ReactNode, type SyntheticEvent } from "react";
import { cn } from "@/lib/cn";
import { Button } from "./button";

export interface DialogProps {
  open: boolean;
  title: ReactNode;
  /** Corpo do diálogo; vira a descrição acessível (aria-describedby). */
  children?: ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  variant?: "danger";
  /** Texto acima do título. Padrão: "Confirmar ação". */
  eyebrow?: ReactNode;
  /**
   * Diálogo de formulário: o botão de confirmar vira o submit do `<form id={formId}>`
   * renderizado nos filhos, o foco começa no primeiro campo e o corpo deixa de
   * ser a descrição acessível. `onConfirm` não é chamado; trate o `onSubmit`.
   */
  formId?: string;
  /** Esconde "Cancelar" (ex.: um único botão "Fechar"); Esc continua chamando `onCancel`. */
  hideCancel?: boolean;
  /** Largura máxima maior, para formulários. */
  size?: "md" | "lg";
  onConfirm?: () => void;
  onCancel: () => void;
  /** Ação em andamento: confirmar fica ocupado e cancelar (botão ou Esc) fica bloqueado. */
  loading?: boolean;
  /** Confirmar indisponível (ex.: confirmação digitada ainda incorreta). */
  confirmDisabled?: boolean;
}

const FOCUSABLE =
  'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])';

/**
 * Diálogo de confirmação modal sobre o `<dialog>` nativo (`showModal()`).
 * O foco começa em "Cancelar", fica preso no diálogo, Esc cancela e, ao fechar,
 * volta para o elemento que abriu o diálogo.
 */
export function Dialog(props: DialogProps) {
  if (!props.open) return null;
  return <OpenDialog {...props} />;
}

function OpenDialog({
  title,
  children,
  confirmLabel,
  cancelLabel = "Cancelar",
  variant,
  eyebrow = "Confirmar ação",
  formId,
  hideCancel = false,
  size = "md",
  onConfirm,
  onCancel,
  loading = false,
  confirmDisabled = false,
}: DialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  // Lido só na abertura: o foco inicial não acompanha mudanças posteriores.
  const isForm = useRef(Boolean(formId));
  const titleId = useId();
  const bodyId = useId();

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    // jsdom (e navegadores antigos) não têm showModal: cai para o atributo `open`.
    if (typeof el.showModal === "function") {
      if (!el.open) el.showModal();
    } else {
      el.setAttribute("open", "");
    }
    const field = isForm.current
      ? bodyRef.current?.querySelector<HTMLElement>("input:not(:disabled), select:not(:disabled), textarea:not(:disabled)")
      : null;
    (field ?? cancelRef.current ?? confirmRef.current)?.focus();
    return () => {
      if (typeof el.close === "function") {
        if (el.open) el.close();
      } else {
        el.removeAttribute("open");
      }
      if (opener?.isConnected) opener.focus();
    };
  }, []);

  // Ao terminar o loading (ex.: erro exibido), o botão desabilitado pode ter
  // jogado o foco para fora: devolve-o ao botão de confirmar.
  useEffect(() => {
    const el = ref.current;
    if (loading || !el) return;
    if (!el.contains(document.activeElement) || document.activeElement === el) confirmRef.current?.focus();
  }, [loading]);

  const cancel = () => {
    if (!loading) onCancel();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDialogElement>) => {
    if (e.key === "Escape") {
      // Impede o fechamento nativo: quem fecha é o dono do estado `open`.
      e.preventDefault();
      cancel();
      return;
    }
    if (e.key !== "Tab") return;
    const el = ref.current;
    if (!el) return;
    const items = [...el.querySelectorAll<HTMLElement>(FOCUSABLE)];
    if (items.length === 0) return;
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    const inside = active instanceof Node && el.contains(active) && active !== el;
    if (!inside) {
      e.preventDefault();
      (e.shiftKey ? last : first).focus();
    } else if (e.shiftKey && active === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  };

  // Evento `cancel` do <dialog> nativo (Esc ou pedido de fechamento do navegador).
  const onNativeCancel = (e: SyntheticEvent<HTMLDialogElement>) => {
    e.preventDefault();
    cancel();
  };

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      aria-describedby={formId ? undefined : bodyId}
      aria-busy={loading || undefined}
      onKeyDown={onKeyDown}
      onCancel={onNativeCancel}
      className={cn(
        "fixed inset-0 m-auto h-fit max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] overflow-y-auto overscroll-contain border border-line-strong bg-surface p-0 text-left text-fg",
        size === "lg" ? "max-w-lg" : "max-w-md",
        "backdrop:bg-black/75",
        variant === "danger" && "shadow-[inset_2px_0_0_0_var(--color-danger)]",
      )}
    >
      <div className="border-b border-line px-5 py-3.5">
        <p
          aria-hidden="true"
          className={cn(
            "font-mono text-[10px] uppercase tracking-[0.12em]",
            variant === "danger" ? "text-danger" : "text-fg-subtle",
          )}
        >
          {eyebrow}
        </p>
        <h2 id={titleId} className="mt-1 text-[15px] font-semibold tracking-tight text-balance">
          {title}
        </h2>
      </div>
      <div ref={bodyRef} id={bodyId} className="space-y-3 px-5 py-4 text-[13px] text-fg-muted">
        {children}
      </div>
      <div className="flex flex-wrap justify-end gap-2 border-t border-line bg-bg/40 px-5 py-3">
        {!hideCancel && (
          <Button ref={cancelRef} variant="secondary" disabled={loading} onClick={cancel}>
            {cancelLabel}
          </Button>
        )}
        <Button
          ref={confirmRef}
          type={formId ? "submit" : "button"}
          form={formId}
          variant={variant === "danger" ? "danger" : "primary"}
          loading={loading}
          disabled={confirmDisabled}
          onClick={formId ? undefined : onConfirm}
        >
          {confirmLabel}
        </Button>
      </div>
    </dialog>
  );
}
