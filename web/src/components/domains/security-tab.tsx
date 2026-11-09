"use client";

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
 * "Modelo segurança": acrescenta os 4 cabeçalhos; um que já exista (mesmo nome,
 * sem diferenciar maiúsculas) recebe o valor do modelo, sem duplicar.
 */
export function applySecurityPreset(list: HeaderDraft[]): HeaderDraft[] {
  const out = list.map((h) => ({ ...h }));
  for (const p of SECURITY_HEADERS) {
    const found = out.find((h) => h.name.trim().toLowerCase() === p.name.toLowerCase());
    if (found) found.value = p.value;
    else if (out.length < MAX_HEADERS) out.push(headerDraft(p));
  }
  return out;
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

  const update = (key: number, f: HeaderField, v: string) => {
    onHeaders(headers.map((h) => (h.key === key ? { ...h, [f]: v } : h)));
    onClearError(`${key}.${f}`);
    onClearError("headers");
  };

  const add = () => {
    if (full) return;
    const h = headerDraft({ name: "", value: "" });
    onHeaders([...headers, h]);
    requestAnimationFrame(() => document.getElementById(headerFieldId(base, h.key, "name"))?.focus());
  };

  const remove = (key: number) => {
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
            <Button size="sm" variant="ghost" onClick={() => onHeaders(applySecurityPreset(headers))}>
              Modelo segurança
            </Button>
            <Button id={`${base}-add-header`} size="sm" disabled={full} onClick={add}>
              Adicionar cabeçalho
            </Button>
          </div>
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
                    onClick={() => remove(h.key)}
                  >
                    Remover<span className="sr-only"> o cabeçalho {n}</span>
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
