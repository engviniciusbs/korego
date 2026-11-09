"use client";

import { useState, type KeyboardEvent } from "react";
import { Alert, Badge, Button, Label } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { useCertificates, type Certificate } from "@/lib/certificates";
import { cn } from "@/lib/cn";
import type { CertInfo, TLSMode } from "@/lib/domains";
import { CertStatus } from "./cert-status";
import { CertificateFields, certFieldId, formatCertDate, useCertificateDraft } from "./certificates-panel";
import { describedBy, FieldError, Hint, selectCls } from "./form-bits";

export interface TlsTabProps {
  base: string;
  mode: TLSMode;
  certId: number | null;
  onMode: (m: TLSMode) => void;
  onCertId: (id: number | null) => void;
  /** Erro de `tls.certId` (do cliente ou do servidor). */
  error?: string;
  onClearError: () => void;
  /** Estado do certificado em uso hoje (domínio já salvo). */
  current?: CertInfo;
}

export const certSelectId = (base: string) => `${base}-certId`;

const MODES: { value: TLSMode; label: string; hint: string }[] = [
  { value: "auto", label: "Automático (Let’s Encrypt)", hint: "Emitido e renovado sozinho; o DNS precisa apontar para este servidor." },
  { value: "custom", label: "Próprio", hint: "Um certificado que você já tem, como um curinga ou o de origem da Cloudflare." },
];

/** Aba Certificado: Let's Encrypt ou um certificado próprio guardado (ou adicionado aqui). */
export function TlsTab({ base, mode, certId, onMode, onCertId, error, onClearError, current }: TlsTabProps) {
  const certs = useCertificates();
  const [adding, setAdding] = useState(false);
  const addBase = `${base}-newcert`;
  const draft = useCertificateDraft((c) => {
    onCertId(c.id);
    onClearError();
    setAdding(false);
    requestAnimationFrame(() => document.getElementById(certSelectId(base))?.focus());
  });
  const list = [...(certs.data ?? [])].sort((a, b) => a.name.localeCompare(b.name));
  const selected = list.find((c) => c.id === certId);
  const errId = `${certSelectId(base)}-error`;

  const submitNew = () => draft.submit((f) => requestAnimationFrame(() => document.getElementById(certFieldId(addBase, f))?.focus()));

  // Enter num campo do formulário embutido guarda o certificado, não o domínio.
  const onAddKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Enter" && e.target instanceof HTMLInputElement) {
      e.preventDefault();
      submitNew();
    }
  };

  return (
    <div className="space-y-4">
      <fieldset>
        <legend className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted">
          Certificado HTTPS
        </legend>
        <div className="mt-1.5 grid gap-px border border-line-strong bg-line-strong sm:grid-cols-2">
          {MODES.map((m) => {
            const id = `${base}-tls-${m.value}`;
            const checked = mode === m.value;
            return (
              <label
                key={m.value}
                htmlFor={id}
                className={cn(
                  "flex cursor-pointer gap-2.5 bg-surface px-3 py-2.5",
                  checked && "bg-surface-2 shadow-[inset_2px_0_0_0_var(--color-accent)]",
                )}
              >
                <input
                  id={id}
                  type="radio"
                  name={`${base}-tls`}
                  value={m.value}
                  checked={checked}
                  aria-describedby={`${id}-hint`}
                  onChange={() => {
                    onMode(m.value);
                    onClearError();
                  }}
                  className={cn(
                    "mt-0.5 size-4 shrink-0 cursor-pointer appearance-none border border-line-strong bg-surface",
                    "checked:border-accent checked:bg-[radial-gradient(var(--color-accent)_0_45%,transparent_46%)]",
                    focusRing,
                  )}
                />
                <span className="min-w-0">
                  <span className="block text-[13px] text-fg">{m.label}</span>
                  <span id={`${id}-hint`} className="mt-0.5 block text-xs text-fg-subtle">
                    {m.hint}
                  </span>
                </span>
              </label>
            );
          })}
        </div>
      </fieldset>

      {mode === "auto" ? (
        current && (
          <div className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
            <span>Hoje:</span>
            <CertStatus cert={current} />
          </div>
        )
      ) : (
        <div className="space-y-3">
          <div>
            <div className="flex flex-wrap items-end justify-between gap-2">
              <Label htmlFor={certSelectId(base)} required>
                Certificado
              </Label>
              {!adding && (
                <Button size="sm" variant="ghost" className="-mr-2" onClick={() => setAdding(true)}>
                  Adicionar certificado
                </Button>
              )}
            </div>
            <select
              id={certSelectId(base)}
              className={cn(selectCls, "mt-1.5")}
              value={certId ?? ""}
              disabled={certs.isPending}
              aria-invalid={error ? true : undefined}
              aria-describedby={describedBy(error && errId)}
              onChange={(e) => {
                onCertId(e.target.value ? Number(e.target.value) : null);
                onClearError();
              }}
            >
              <option value="">{certs.isPending ? "Carregando…" : list.length ? "Selecione…" : "Nenhum guardado"}</option>
              {list.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
            <FieldError id={errId}>{error}</FieldError>
            {certs.isError && <Hint>Não foi possível listar os certificados: {certs.error.message}</Hint>}
          </div>

          {adding && (
            <div
              role="group"
              aria-labelledby={`${addBase}-title`}
              onKeyDown={onAddKeyDown}
              className="border border-line bg-bg/30 p-3"
            >
              <p id={`${addBase}-title`} className="mb-3 text-[13px] font-medium text-fg">
                Novo certificado
              </p>
              <CertificateFields base={addBase} draft={draft} />
              <div className="mt-3 flex flex-wrap justify-end gap-2">
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={draft.pending}
                  onClick={() => {
                    draft.reset();
                    setAdding(false);
                  }}
                >
                  Cancelar
                </Button>
                <Button size="sm" variant="primary" loading={draft.pending} onClick={submitNew}>
                  Guardar certificado
                </Button>
              </div>
            </div>
          )}

          {selected && <CertSummary cert={selected} />}
        </div>
      )}
    </div>
  );
}

function CertSummary({ cert }: { cert: Certificate }) {
  const row = "grid gap-0.5 sm:grid-cols-[7.5rem_minmax(0,1fr)] sm:gap-3";
  const dt = "text-xs text-fg-subtle";
  return (
    <div className="space-y-2">
      {cert.expiresSoon && (
        <Alert variant="warning">Este certificado vence em {formatCertDate(cert.notAfter)}. Troque-o antes disso.</Alert>
      )}
      <dl className="space-y-1.5 border border-line px-3 py-2.5 text-[13px]">
        <div className={row}>
          <dt className={dt}>Emissor</dt>
          <dd className="text-fg [overflow-wrap:anywhere]">{cert.issuer}</dd>
        </div>
        <div className={row}>
          <dt className={dt}>Nomes</dt>
          <dd className="font-mono text-xs text-fg [overflow-wrap:anywhere]" translate="no">
            {cert.names.join(", ")}
          </dd>
        </div>
        <div className={row}>
          <dt className={dt}>Válido até</dt>
          <dd className="flex flex-wrap items-center gap-1.5 font-mono text-xs tabular-nums text-fg">
            {formatCertDate(cert.notAfter)}
            {cert.expiresSoon && (
              <Badge size="sm" variant="warning" dot>
                Vence em breve
              </Badge>
            )}
          </dd>
        </div>
        <div className={row}>
          <dt className={dt}>SHA-256</dt>
          <dd className="font-mono text-[11px] text-fg-muted [overflow-wrap:anywhere]" translate="no">
            {cert.fingerprint}
          </dd>
        </div>
      </dl>
    </div>
  );
}
