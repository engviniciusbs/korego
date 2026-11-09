"use client";

import { useState } from "react";
import { Button, Input } from "@/components/ui";
import { MAX_HEADERS, SECURITY_HEADERS, type DomainHeader } from "@/lib/domains";
import { validHeaderName } from "@/lib/validate";
import { describedBy, FieldError, Hint, Switch } from "./form-bits";

export interface HeaderDraft extends DomainHeader {
  key: number;
}

export type HeaderField = "name" | "value";

let nextKey = 1;
export const headerDraft = (h: DomainHeader): HeaderDraft => ({ key: nextKey++, name: h.name, value: h.value });

/**
 * "Modelo segurança": acrescenta só os cabeçalhos do modelo que faltam. Um que
 * já exista (mesmo nome, sem diferenciar maiúsculas) mantém o valor atual, e a
 * nota diz isso. Se os que faltam passarem do limite, nada é acrescentado e a
 * nota explica; nenhum cabeçalho é descartado.
 */
export function applySecurityPreset(list: HeaderDraft[]): { headers: HeaderDraft[]; notes: string[] } {
  const notes: string[] = [];
  const missing: DomainHeader[] = [];
  for (const p of SECURITY_HEADERS) {
    const found = list.find((h) => h.name.trim().toLowerCase() === p.name.toLowerCase());
    if (!found) missing.push(p);
    else if (found.value !== p.value) notes.push(`Mantido o valor atual de ${found.name.trim()}.`);
  }
  if (list.length + missing.length > MAX_HEADERS) {
    const free = MAX_HEADERS - list.length;
    return {
      headers: list,
      notes: [
        `O modelo acrescentaria ${missing.length} cabeçalhos, mas o limite de ${MAX_HEADERS} deixa ${free} vaga${free === 1 ? "" : "s"}. Remova ${missing.length - free} e aplique de novo.`,
      ],
    };
  }
  if (missing.length === 0 && notes.length === 0) notes.push("Os cabeçalhos do modelo já estão na lista.");
  return { headers: [...list, ...missing.map(headerDraft)], notes };
}

/** Confere os cabeçalhos como o servidor. */
export function validateHeaders(list: HeaderDraft[]): { key: number; field: HeaderField; message: string }[] {
  const out: { key: number; field: HeaderField; message: string }[] = [];
  const seen = new Set<string>();
  for (const h of list) {
    const n = h.name.trim();
    if (!validHeaderName(n)) {
      out.push({ key: h.key, field: "name", message: "Nome inválido. Use letras, números e hífens, sem espaços." });
    } else if (seen.has(n.toLowerCase())) {
      out.push({ key: h.key, field: "name", message: `O cabeçalho ${n} aparece mais de uma vez.` });
    }
    seen.add(n.toLowerCase());
    if (/[\r\n]/.test(h.value)) {
      out.push({ key: h.key, field: "value", message: "O valor não pode ter quebra de linha." });
    }
  }
  return out;
}

export interface SecurityTabProps {
  base: string;
  cloudflareOnly: boolean;
  onCloudflareOnly: (v: boolean) => void;
  headers: HeaderDraft[];
  onHeaders: (h: HeaderDraft[]) => void;
  compression: boolean;
  onCompression: (v: boolean) => void;
  /** Erros por `${key}.${campo}`, mais `cloudflareOnly` e `headers` (lista). */
  errors: Record<string, string | undefined>;
  onClearError: (k: string) => void;
}

export const headerFieldId = (base: string, key: number, f: HeaderField) => `${base}-h${key}-${f}`;
export const cfSwitchId = (base: string) => `${base}-cf`;

/** Aba Segurança: só Cloudflare, cabeçalhos de resposta e compressão. */
export function SecurityTab({
  base,
  cloudflareOnly,
  onCloudflareOnly,
  headers,
  onHeaders,
  compression,
  onCompression,
  errors,
  onClearError,
}: SecurityTabProps) {
  const full = headers.length >= MAX_HEADERS;
  const cfErr = errors.cloudflareOnly;
  const [notes, setNotes] = useState<string[]>([]);

  const applyPreset = () => {
    const r = applySecurityPreset(headers);
    onHeaders(r.headers);
    setNotes(r.notes);
  };

  const update = (key: number, f: HeaderField, v: string) => {
    setNotes([]);
    onHeaders(headers.map((h) => (h.key === key ? { ...h, [f]: v } : h)));
    onClearError(`${key}.${f}`);
    onClearError("headers");
  };

  const add = () => {
    if (full) return;
    setNotes([]);
    const h = headerDraft({ name: "", value: "" });
    onHeaders([...headers, h]);
    requestAnimationFrame(() => document.getElementById(headerFieldId(base, h.key, "name"))?.focus());
  };

  const remove = (key: number) => {
    setNotes([]);
    onHeaders(headers.filter((h) => h.key !== key));
    onClearError("headers");
    requestAnimationFrame(() => document.getElementById(`${base}-add-header`)?.focus());
  };

  return (
    <div className="space-y-5">
      <div>
        <Switch
          id={cfSwitchId(base)}
          checked={cloudflareOnly}
          invalid={Boolean(cfErr)}
          describedBy={describedBy(cfErr && `${cfSwitchId(base)}-error`, `${cfSwitchId(base)}-hint`)}
          onChange={(v) => {
            onCloudflareOnly(v);
            onClearError("cloudflareOnly");
          }}
        >
          Só Cloudflare
        </Switch>
        <FieldError id={`${cfSwitchId(base)}-error`}>{cfErr}</FieldError>
        <Hint id={`${cfSwitchId(base)}-hint`} className="mt-0 pl-10">
          Recusa (403) quem não chega pela Cloudflare. O IP do visitante vem do CF-Connecting-IP.
        </Hint>
      </div>

      <div>
        <Switch
          checked={compression}
          describedBy={`${base}-compression-hint`}
          onChange={onCompression}
        >
          Compressão
        </Switch>
        <Hint id={`${base}-compression-hint`} className="mt-0 pl-10">
          Respostas em zstd ou gzip, conforme o navegador aceitar.
        </Hint>
      </div>

      <fieldset aria-labelledby={`${base}-headers-title`} className="border-t border-line pt-4">
        <div className="flex flex-wrap items-end justify-between gap-2">
          <div>
            <p id={`${base}-headers-title`} className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted">
              Cabeçalhos de resposta{" "}
              <span className="text-fg-subtle tabular-nums">
                {headers.length}/{MAX_HEADERS}
              </span>
            </p>
            <Hint id={`${base}-headers-hint`} className="mt-0.5">
              Enviados em todas as rotas do domínio.
            </Hint>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="ghost" onClick={applyPreset}>
              Modelo segurança
            </Button>
            <Button id={`${base}-add-header`} size="sm" disabled={full} onClick={add}>
              Adicionar cabeçalho
            </Button>
          </div>
        </div>
        <div role="status" aria-live="polite">
          {notes.length > 0 && (
            <ul className="mt-2 space-y-0.5 border-l-2 border-info pl-2 text-xs text-fg-muted">
              {notes.map((n) => (
                <li key={n}>{n}</li>
              ))}
            </ul>
          )}
        </div>
        {errors.headers && (
          <p role="alert" className="mt-2 text-xs text-danger">
            {errors.headers}
          </p>
        )}
        {headers.length === 0 ? (
          <p className="mt-3 border border-dashed border-line px-3 py-3 text-xs text-fg-subtle">
            Nenhum cabeçalho. &ldquo;Modelo segurança&rdquo; acrescenta HSTS, nosniff, Referrer-Policy e
            Permissions-Policy.
          </p>
        ) : (
          <ol className="mt-3 space-y-2">
            {headers.map((h, i) => {
              const n = i + 1;
              const nameErr = errors[`${h.key}.name`];
              const valueErr = errors[`${h.key}.value`];
              return (
                <li
                  key={h.key}
                  className="grid gap-1.5 border-l-2 border-line pl-2 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto] sm:items-start sm:border-l-0 sm:pl-0"
                >
                  <div className="min-w-0">
                    <Input
                      id={headerFieldId(base, h.key, "name")}
                      size="sm"
                      mono
                      aria-label={`Nome do cabeçalho ${n}`}
                      value={h.name}
                      placeholder="X-Frame-Options"
                      autoComplete="off"
                      spellCheck={false}
                      autoCapitalize="none"
                      invalid={Boolean(nameErr)}
                      aria-describedby={describedBy(nameErr && `${headerFieldId(base, h.key, "name")}-error`)}
                      onChange={(e) => update(h.key, "name", e.target.value)}
                    />
                    <FieldError id={`${headerFieldId(base, h.key, "name")}-error`}>{nameErr}</FieldError>
                  </div>
                  <div className="min-w-0">
                    <Input
                      id={headerFieldId(base, h.key, "value")}
                      size="sm"
                      mono
                      aria-label={`Valor do cabeçalho ${n}`}
                      value={h.value}
                      placeholder="DENY"
                      autoComplete="off"
                      spellCheck={false}
                      autoCapitalize="none"
                      title={h.value.length > 40 ? h.value : undefined}
                      invalid={Boolean(valueErr)}
                      aria-describedby={describedBy(valueErr && `${headerFieldId(base, h.key, "value")}-error`)}
                      onChange={(e) => update(h.key, "value", e.target.value)}
                    />
                    <FieldError id={`${headerFieldId(base, h.key, "value")}-error`}>{valueErr}</FieldError>
                  </div>
                  <Button
                    size="sm"
                    variant="ghost"
                    className="justify-self-start enabled:hover:bg-danger-bg enabled:hover:text-danger"
                    aria-label={`Remover o cabeçalho ${n}`}
                    onClick={() => remove(h.key)}
                  >
                    Remover
                  </Button>
                </li>
              );
            })}
          </ol>
        )}
      </fieldset>
    </div>
  );
}
