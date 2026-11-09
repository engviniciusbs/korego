"use client";

import { useState } from "react";
import { PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Dialog, Input, Label, Panel, Spinner, Tabs, tabId, tabPanelId } from "@/components/ui";
import { ApiError } from "@/lib/api";
import {
  useAddCertificate,
  useCertificates,
  useDeleteCertificate,
  type Certificate,
  type CertificateInput,
} from "@/lib/certificates";
import { cn } from "@/lib/cn";
import { describedBy, FieldError, Hint } from "./form-bits";

export const certDateFmt = new Intl.DateTimeFormat("pt-BR", {
  day: "2-digit",
  month: "2-digit",
  year: "numeric",
  timeZone: "UTC",
});

/** Data de validade em dd/mm/aaaa (UTC), ou "" se ausente/inválida. */
export function formatCertDate(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 1970) return "";
  return certDateFmt.format(d);
}

type Source = "pem" | "path";
type CertField = "name" | "pem" | "path";

const SOURCES: { value: Source; label: string }[] = [
  { value: "pem", label: "Colar PEM" },
  { value: "path", label: "Arquivo no servidor" },
];

/**
 * Estado e envio do formulário "adicionar certificado". O PEM some do estado
 * assim que o certificado é guardado; a resposta nunca traz a chave.
 */
export function useCertificateDraft(onAdded: (c: Certificate) => void) {
  const add = useAddCertificate();
  const [name, setName] = useState("");
  const [source, setSource] = useState<Source>("pem");
  const [pem, setPem] = useState("");
  const [path, setPath] = useState("");
  const [errors, setErrors] = useState<Partial<Record<CertField, string>>>({});
  const [formError, setFormError] = useState("");

  const reset = () => {
    setName("");
    setPem("");
    setPath("");
    setErrors({});
    setFormError("");
    add.reset();
  };

  /** Valida e envia; devolve o primeiro campo com erro (para o foco), se houver. */
  const submit = (focus: (f: CertField) => void) => {
    if (add.isPending) return;
    const next: Partial<Record<CertField, string>> = {};
    if (!name.trim()) next.name = "Dê um nome ao certificado.";
    if (source === "pem" && !pem.trim()) next.pem = "Cole a cadeia do certificado e a chave privada, em PEM.";
    if (source === "path" && !path.trim().startsWith("/")) next.path = "Use um caminho absoluto, como /etc/ssl/loja.pem.";
    setErrors(next);
    setFormError("");
    const first = (["name", "pem", "path"] as CertField[]).find((f) => next[f]);
    if (first) {
      focus(first);
      return;
    }
    const input: CertificateInput =
      source === "pem" ? { name: name.trim(), pem } : { name: name.trim(), path: path.trim() };
    add.mutate(input, {
      onSuccess: (c) => {
        reset();
        onAdded(c);
      },
      onError: (err) => {
        if (err instanceof ApiError && ["INVALID_CERT", "CERT_MISMATCH", "CERT_EXPIRED"].includes(err.code)) {
          setErrors({ [source]: err.message });
          focus(source);
        } else {
          setFormError(err.message);
        }
      },
    });
  };

  return {
    name,
    setName,
    source,
    setSource,
    pem,
    setPem,
    path,
    setPath,
    errors,
    clear: (f: CertField) => setErrors((e) => (e[f] ? { ...e, [f]: undefined } : e)),
    formError,
    pending: add.isPending,
    submit,
    reset,
  };
}

export type CertificateDraft = ReturnType<typeof useCertificateDraft>;

export const certFieldId = (base: string, f: CertField) => `${base}-${f}`;

/** Campos do formulário de certificado (nome, PEM colado ou caminho no servidor). */
export function CertificateFields({ base, draft }: { base: string; draft: CertificateDraft }) {
  const id = (f: CertField) => certFieldId(base, f);
  const errId = (f: CertField) => `${id(f)}-error`;
  const err = draft.errors;
  const tabsId = `${base}-source`;
  return (
    <div className="space-y-3">
      {draft.formError && <Alert variant="danger">{draft.formError}</Alert>}
      <div>
        <Label htmlFor={id("name")} required>
          Nome
        </Label>
        <Input
          id={id("name")}
          className="mt-1.5"
          value={draft.name}
          placeholder="curinga exemplo.com…"
          autoComplete="off"
          maxLength={100}
          invalid={Boolean(err.name)}
          aria-describedby={describedBy(err.name && errId("name"))}
          onChange={(e) => {
            draft.setName(e.target.value);
            draft.clear("name");
          }}
        />
        <FieldError id={errId("name")}>{err.name}</FieldError>
      </div>
      <div>
        <Tabs
          id={tabsId}
          label="Origem do certificado"
          items={SOURCES}
          value={draft.source}
          onChange={draft.setSource}
        />
        <div role="tabpanel" id={tabPanelId(tabsId)} aria-labelledby={tabId(tabsId, draft.source)} className="mt-3">
          {draft.source === "pem" ? (
            <div>
              <Label htmlFor={id("pem")} required>
                Certificado e chave (PEM)
              </Label>
              <textarea
                id={id("pem")}
                value={draft.pem}
                rows={6}
                spellCheck={false}
                autoComplete="off"
                autoCapitalize="none"
                placeholder={"-----BEGIN CERTIFICATE-----\n…\n-----BEGIN PRIVATE KEY-----\n…"}
                aria-invalid={err.pem ? true : undefined}
                aria-describedby={describedBy(err.pem && errId("pem"), `${id("pem")}-hint`)}
                onChange={(e) => {
                  draft.setPem(e.target.value);
                  draft.clear("pem");
                }}
                className={cn(
                  "mt-1.5 block w-full min-w-0 resize-y border border-line-strong bg-surface px-3 py-2 font-mono text-xs text-fg",
                  "placeholder:text-fg-subtle outline-hidden hover:border-fg-subtle",
                  "focus-visible:border-accent-text focus-visible:bg-surface-2 focus-visible:shadow-[inset_2px_0_0_0_var(--color-accent-text)]",
                  "aria-invalid:border-danger",
                )}
              />
              <FieldError id={errId("pem")}>{err.pem}</FieldError>
              <Hint id={`${id("pem")}-hint`}>
                A cadeia completa e a chave privada. A chave é guardada cifrada e nunca é mostrada de novo.
              </Hint>
            </div>
          ) : (
            <div>
              <Label htmlFor={id("path")} required>
                Caminho no servidor
              </Label>
              <Input
                id={id("path")}
                mono
                className="mt-1.5"
                value={draft.path}
                placeholder="/etc/ssl/loja.pem"
                autoComplete="off"
                spellCheck={false}
                autoCapitalize="none"
                invalid={Boolean(err.path)}
                aria-describedby={describedBy(err.path && errId("path"), `${id("path")}-hint`)}
                onChange={(e) => {
                  draft.setPath(e.target.value);
                  draft.clear("path");
                }}
              />
              <FieldError id={errId("path")}>{err.path}</FieldError>
              <Hint id={`${id("path")}-hint`}>
                Um arquivo PEM com a cadeia e a chave. É lido uma vez, agora; depois o arquivo pode mudar ou sumir.
              </Hint>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

const focusById = (id: string) => requestAnimationFrame(() => document.getElementById(id)?.focus());

function AddCertificateDialog({ onClose }: { onClose: () => void }) {
  const base = "cert-add";
  const formId = `${base}-form`;
  const draft = useCertificateDraft(() => onClose());
  return (
    <Dialog
      open
      size="lg"
      eyebrow="Certificados"
      title="Adicionar certificado"
      confirmLabel="Guardar certificado"
      formId={formId}
      loading={draft.pending}
      onCancel={onClose}
    >
      <form
        id={formId}
        noValidate
        className="text-fg"
        onSubmit={(e) => {
          e.preventDefault();
          draft.submit((f) => focusById(certFieldId(base, f)));
        }}
      >
        <CertificateFields base={base} draft={draft} />
      </form>
    </Dialog>
  );
}

const th = "px-3 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const td = "px-3 py-2.5 align-top";

/** Seção "Certificados": os certificados próprios guardados, onde são usados e quando vencem. */
export function CertificatesPanel() {
  const certs = useCertificates();
  const del = useDeleteCertificate();
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<Certificate | null>(null);

  const list = [...(certs.data ?? [])].sort((a, b) => a.name.localeCompare(b.name));

  return (
    <>
      <Panel
        title="Certificados"
        description={
          certs.isSuccess
            ? `${list.length} guardado${list.length === 1 ? "" : "s"}. Usados pelos domínios com certificado próprio.`
            : undefined
        }
        size="sm"
        className="[&>div]:p-0"
        actions={
          <Button size="sm" onClick={() => setAdding(true)}>
            Adicionar certificado
          </Button>
        }
      >
        {certs.isPending ? (
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">Carregando certificados…</span>
          </PanelState>
        ) : certs.isError ? (
          <div className="p-3">
            <Alert variant="danger" title="Não foi possível listar os certificados.">
              <p>{certs.error.message}</p>
              <Button size="sm" className="mt-2" onClick={() => void certs.refetch()}>
                Tentar novamente
              </Button>
            </Alert>
          </div>
        ) : list.length === 0 ? (
          <PanelState>
            <span>Nenhum certificado próprio. Os domínios usam o Let’s Encrypt.</span>
          </PanelState>
        ) : (
          <div className="relative overflow-x-auto">
            <table className="w-full min-w-[720px] text-left text-[13px]">
              <thead>
                <tr>
                  <th scope="col" className={th}>Nome</th>
                  <th scope="col" className={th}>Nomes cobertos</th>
                  <th scope="col" className={th}>Validade</th>
                  <th scope="col" className={th}>Em uso por</th>
                  <th scope="col" className={cn(th, "text-right")}>
                    <span className="sr-only">Ações</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.map((c) => (
                  <tr key={c.id} className="border-t border-line">
                    <td className={cn(td, "min-w-40")}>
                      <span className="font-medium text-fg [overflow-wrap:anywhere]">{c.name}</span>
                      <span className="mt-0.5 block text-xs text-fg-subtle [overflow-wrap:anywhere]">{c.issuer}</span>
                    </td>
                    <td className={cn(td, "max-w-64 font-mono text-xs text-fg-muted")} translate="no">
                      <span className="[overflow-wrap:anywhere]">{c.names.join(", ")}</span>
                    </td>
                    <td className={td}>
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="font-mono text-xs tabular-nums text-fg">{formatCertDate(c.notAfter)}</span>
                        {c.expiresSoon && (
                          <Badge size="sm" variant="warning" dot>
                            Vence em breve
                          </Badge>
                        )}
                      </div>
                    </td>
                    <td className={cn(td, "font-mono text-xs")} translate="no">
                      {c.usedBy.length === 0 ? (
                        <span className="font-sans text-fg-subtle">Nenhum domínio</span>
                      ) : (
                        <ul className="space-y-0.5">
                          {c.usedBy.map((h) => (
                            <li key={h} className="text-fg [overflow-wrap:anywhere]">
                              {h}
                            </li>
                          ))}
                        </ul>
                      )}
                    </td>
                    <td className={cn(td, "text-right")}>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="enabled:hover:bg-danger-bg enabled:hover:text-danger"
                        onClick={() => {
                          del.reset();
                          setRemoving(c);
                        }}
                      >
                        Apagar<span className="sr-only"> o certificado {c.name}</span>
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {adding && <AddCertificateDialog onClose={() => setAdding(false)} />}

      <Dialog
        open={removing !== null}
        title="Apagar certificado?"
        confirmLabel="Apagar"
        variant="danger"
        loading={del.isPending}
        onConfirm={() => removing && del.mutate(removing.id, { onSuccess: () => setRemoving(null) })}
        onCancel={() => setRemoving(null)}
      >
        <p>
          <span className="text-fg">{removing?.name}</span> e a chave guardada são apagados. Um certificado em uso
          não pode ser apagado.
        </p>
        {del.isError && <Alert variant="danger">{del.error.message}</Alert>}
      </Dialog>
    </>
  );
}
