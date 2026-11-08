"use client";

import { useId, useState, type FormEvent, type ReactNode } from "react";
import { Alert, Dialog, Input, Label, Tabs, tabId, tabPanelId } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { ApiError } from "@/lib/api";
import { useApps } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { useSaveDomain, type DnsCheck, type Domain, type DomainInput } from "@/lib/domains";
import { validAddress, validHostname } from "@/lib/validate";

type Field = "hostname" | "appId" | "address" | "wwwRedirect";
type Target = DomainInput["targetType"];

export const WWW_OPTIONS = [
  { value: "none", label: "Nenhum" },
  { value: "www_to_apex", label: "www → principal" },
  { value: "apex_to_www", label: "principal → www" },
] as const;

const TARGETS: { value: Target; label: string }[] = [
  { value: "app", label: "Aplicação" },
  { value: "address", label: "Endereço" },
];

/** Espelha internal/domains.wwwMismatch. */
function wwwMismatch(hostname: string, www: string): string {
  if (www === "www_to_apex" && hostname.startsWith("www.")) {
    return "Para redirecionar www para o domínio sem www, cadastre o domínio sem www.";
  }
  if (www === "apex_to_www" && !hostname.startsWith("www.")) {
    return "Para redirecionar para www, cadastre o domínio com www.";
  }
  return "";
}

/** Erro da API → campo (quando houver) e mensagem em português. */
export function domainErrorMessage(err: unknown): { field?: Field; message: string } {
  if (!(err instanceof ApiError)) return { message: err instanceof Error ? err.message : "Erro desconhecido." };
  switch (err.code) {
    case "INVALID_DOMAIN": {
      const f = err.field === "targetType" ? undefined : (err.field as Field | undefined);
      return { field: f, message: err.message };
    }
    case "DOMAIN_EXISTS":
      return { field: "hostname", message: "Este domínio já está cadastrado." };
    case "PANEL_DOMAIN_PROTECTED":
      return { message: "Este é o domínio do painel; ele é gerenciado em Configurações → Painel." };
    default:
      return { message: err.message };
  }
}

/** Hostnames servidos por um domínio: ele e, com redirecionamento www, o par. */
function hostsOf(hostname: string, www: string): string[] {
  if (www === "www_to_apex") return [hostname, `www.${hostname}`];
  if (www === "apex_to_www") return [hostname, hostname.replace(/^www\./, "")];
  return [hostname];
}

const selectCls = cn(
  "block h-9 w-full min-w-0 border border-line-strong bg-surface px-3 text-[13px] text-fg",
  "outline-hidden hover:border-fg-subtle focus-visible:border-accent-text focus-visible:bg-surface-2",
  "focus-visible:shadow-[inset_2px_0_0_0_var(--color-accent-text)]",
  "aria-invalid:border-danger disabled:cursor-not-allowed disabled:opacity-50",
);

function FieldError({ id, children }: { id: string; children?: string }) {
  if (!children) return null;
  return (
    <p id={id} className="mt-1 text-xs text-danger [overflow-wrap:anywhere]">
      {children}
    </p>
  );
}

function Hint({ id, children }: { id?: string; children: ReactNode }) {
  return (
    <p id={id} className="mt-1 text-xs text-fg-subtle">
      {children}
    </p>
  );
}

export interface DomainDialogProps {
  /** Domínio em edição; sem ele, cria um novo. */
  domain?: Domain;
  onClose: () => void;
}

/**
 * Formulário de domínio em diálogo. Valida no cliente antes de enviar. Salvo com
 * DNS errado, o diálogo continua aberto mostrando o registro A esperado; o
 * domínio já está gravado.
 */
export function DomainDialog({ domain, onClose }: DomainDialogProps) {
  const uid = useId();
  const id = (f: string) => `${uid}-${f}`;
  const errId = (f: Field) => `${uid}-${f}-error`;
  const formId = id("form");

  const apps = useApps();
  const save = useSaveDomain();

  const [hostname, setHostname] = useState(domain?.hostname ?? "");
  const [target, setTarget] = useState<Target>(domain?.targetType ?? "app");
  const [appId, setAppId] = useState<string>(domain?.appId ? String(domain.appId) : "");
  const [address, setAddress] = useState(domain?.address ?? "");
  const [www, setWww] = useState(domain?.wwwRedirect || "none");
  const [forceHttps, setForceHttps] = useState(domain?.forceHttps ?? true);
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const [formError, setFormError] = useState("");
  const [warning, setWarning] = useState<{ hosts: string[]; dns: DnsCheck } | null>(null);

  const clear = (f: Field) => setErrors((e) => (e[f] ? { ...e, [f]: undefined } : e));

  const focusField = (f: Field) => {
    if (f === "appId" || f === "address") {
      const t: Target = f === "appId" ? "app" : "address";
      setTarget(t);
    }
    // Espera o painel da aba renderizar.
    requestAnimationFrame(() => document.getElementById(id(f))?.focus());
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (save.isPending) return;
    const host = hostname.trim().toLowerCase();
    const next: Partial<Record<Field, string>> = {};
    if (!validHostname(host)) {
      next.hostname = "Hostname inválido. Use letras minúsculas, números e hífens, como loja.exemplo.com.";
    }
    if (target === "app" && !appId) next.appId = "Escolha a aplicação.";
    if (target === "address" && !validAddress(address.trim())) {
      next.address = "Endereço inválido. Use host:porta, como 127.0.0.1:5000.";
    }
    const mismatch = !next.hostname ? wwwMismatch(host, www) : "";
    if (mismatch) next.wwwRedirect = mismatch;
    setErrors(next);
    setFormError("");
    const first = (["hostname", "appId", "address", "wwwRedirect"] as Field[]).find((f) => next[f]);
    if (first) {
      focusField(first);
      return;
    }

    const input: DomainInput = {
      hostname: host,
      targetType: target,
      appId: target === "app" ? Number(appId) : null,
      address: target === "address" ? address.trim() : "",
      wwwRedirect: www,
      forceHttps,
    };
    save.mutate(
      { id: domain?.id, input },
      {
        onSuccess: (r) => {
          if (r.dns.ok) onClose();
          else setWarning({ hosts: hostsOf(r.domain.hostname, r.domain.wwwRedirect), dns: r.dns });
        },
        onError: (err) => {
          const m = domainErrorMessage(err);
          if (m.field) {
            setErrors({ [m.field]: m.message });
            focusField(m.field);
          } else {
            setFormError(m.message);
          }
        },
      },
    );
  };

  const title = domain ? "Editar domínio" : "Adicionar domínio";

  if (warning) {
    return (
      <Dialog
        open
        size="lg"
        eyebrow="Domínio salvo"
        title={title}
        confirmLabel="Fechar"
        hideCancel
        onConfirm={onClose}
        onCancel={onClose}
      >
        <DnsWarning hosts={warning.hosts} dns={warning.dns} />
      </Dialog>
    );
  }

  const describe = (f: Field, hint?: string) => [errors[f] ? errId(f) : "", hint ?? ""].filter(Boolean).join(" ") || undefined;
  const appList = apps.data ?? [];

  return (
    <Dialog
      open
      size="lg"
      eyebrow={domain ? "Domínio" : "Novo domínio"}
      title={title}
      confirmLabel="Salvar"
      formId={formId}
      loading={save.isPending}
      onCancel={onClose}
    >
      <form id={formId} noValidate onSubmit={submit} className="space-y-4 text-fg">
        {formError && <Alert variant="danger">{formError}</Alert>}

        <div>
          <Label htmlFor={id("hostname")} required>
            Hostname
          </Label>
          <Input
            id={id("hostname")}
            mono
            className="mt-1.5"
            value={hostname}
            placeholder="loja.exemplo.com…"
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="none"
            inputMode="url"
            invalid={Boolean(errors.hostname)}
            aria-describedby={describe("hostname")}
            onChange={(e) => {
              setHostname(e.target.value);
              clear("hostname");
              clear("wwwRedirect");
            }}
          />
          <FieldError id={errId("hostname")}>{errors.hostname}</FieldError>
        </div>

        <fieldset>
          <legend className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted">
            Destino
          </legend>
          <Tabs
            id={id("target")}
            label="Tipo de destino"
            items={TARGETS}
            value={target}
            onChange={setTarget}
            className="mt-1.5"
          />
          <div
            role="tabpanel"
            id={tabPanelId(id("target"))}
            aria-labelledby={tabId(id("target"), target)}
            className="mt-3"
          >
            {target === "app" ? (
              <div>
                <Label htmlFor={id("appId")} required>
                  Aplicação
                </Label>
                <select
                  id={id("appId")}
                  className={cn(selectCls, "mt-1.5 font-mono")}
                  value={appId}
                  disabled={apps.isPending}
                  aria-invalid={errors.appId ? true : undefined}
                  aria-describedby={describe("appId", appList.length === 0 ? id("app-hint") : undefined)}
                  onChange={(e) => {
                    setAppId(e.target.value);
                    clear("appId");
                  }}
                >
                  <option value="">{apps.isPending ? "Carregando…" : "Selecione…"}</option>
                  {appList.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.slug}
                    </option>
                  ))}
                </select>
                <FieldError id={errId("appId")}>{errors.appId}</FieldError>
                {apps.isError && <Hint>Não foi possível listar as aplicações: {apps.error.message}</Hint>}
                {apps.isSuccess && appList.length === 0 && (
                  <Hint id={id("app-hint")}>Nenhuma aplicação cadastrada. Use um endereço host:porta.</Hint>
                )}
              </div>
            ) : (
              <div>
                <Label htmlFor={id("address")} required>
                  Endereço (host:porta)
                </Label>
                <Input
                  id={id("address")}
                  mono
                  className="mt-1.5"
                  value={address}
                  placeholder="127.0.0.1:5000"
                  autoComplete="off"
                  spellCheck={false}
                  autoCapitalize="none"
                  invalid={Boolean(errors.address)}
                  aria-describedby={describe("address", id("address-hint"))}
                  onChange={(e) => {
                    setAddress(e.target.value);
                    clear("address");
                  }}
                />
                <FieldError id={errId("address")}>{errors.address}</FieldError>
                <Hint id={id("address-hint")}>Um serviço que já roda no servidor, fora do KoreGO.</Hint>
              </div>
            )}
          </div>
        </fieldset>

        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <Label htmlFor={id("wwwRedirect")}>Redirecionamento www</Label>
            <select
              id={id("wwwRedirect")}
              className={cn(selectCls, "mt-1.5")}
              value={www}
              aria-invalid={errors.wwwRedirect ? true : undefined}
              aria-describedby={describe("wwwRedirect")}
              onChange={(e) => {
                setWww(e.target.value);
                clear("wwwRedirect");
              }}
            >
              {WWW_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            <FieldError id={errId("wwwRedirect")}>{errors.wwwRedirect}</FieldError>
          </div>
          <div className="flex items-end">
            <label className="flex h-9 cursor-pointer items-center gap-2.5 text-[13px] text-fg">
              <span className="relative inline-flex size-4 shrink-0">
                <input
                  type="checkbox"
                  checked={forceHttps}
                  onChange={(e) => setForceHttps(e.target.checked)}
                  className={cn(
                    "peer size-4 cursor-pointer appearance-none border border-line-strong bg-surface",
                    "checked:border-accent checked:bg-accent",
                    focusRing,
                  )}
                />
                <svg
                  aria-hidden="true"
                  viewBox="0 0 16 16"
                  className="pointer-events-none absolute inset-0 hidden size-4 text-accent-fg peer-checked:block"
                >
                  <path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square" />
                </svg>
              </span>
              Forçar HTTPS
            </label>
          </div>
        </div>
      </form>
    </Dialog>
  );
}

function DnsWarning({ hosts, dns }: { hosts: string[]; dns: DnsCheck }) {
  return (
    <div className="space-y-3">
      <Alert variant="warning" title="O DNS ainda não aponta para este servidor.">
        <p>{dns.message}</p>
      </Alert>
      {dns.expected.length > 0 ? (
        <>
          <p>Crie no seu provedor de DNS o registro abaixo. O certificado é emitido assim que ele propagar.</p>
          <div className="overflow-x-auto border border-line">
            <table className="w-full font-mono text-xs">
              <thead>
                <tr className="bg-surface-2 text-left text-[10px] uppercase tracking-[0.12em] text-fg-subtle">
                  <th scope="col" className="px-3 py-1.5 font-medium">Tipo</th>
                  <th scope="col" className="px-3 py-1.5 font-medium">Nome</th>
                  <th scope="col" className="px-3 py-1.5 font-medium">Valor</th>
                </tr>
              </thead>
              <tbody translate="no">
                {hosts.flatMap((h) =>
                  dns.expected.map((ip) => (
                    <tr key={`${h}-${ip}`} className="border-t border-line text-fg">
                      <td className="px-3 py-1.5 text-accent-text">{ip.includes(":") ? "AAAA" : "A"}</td>
                      <td className="px-3 py-1.5 [overflow-wrap:anywhere]">{h}</td>
                      <td className="px-3 py-1.5">{ip}</td>
                    </tr>
                  )),
                )}
              </tbody>
            </table>
          </div>
        </>
      ) : (
        <p>Não foi possível descobrir o IP público deste servidor; aponte o domínio para o IP da VPS.</p>
      )}
      {dns.resolved.length > 0 && (
        <p className="text-xs text-fg-subtle">
          Hoje resolve para: <span className="font-mono text-fg-muted" translate="no">{dns.resolved.join(", ")}</span>
        </p>
      )}
    </div>
  );
}
