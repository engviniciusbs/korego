"use client";

import { useId, useState, type FormEvent } from "react";
import { Alert, Dialog, Input, Label, Tabs, tabId, tabPanelId } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { useApps } from "@/lib/apps";
import { cn } from "@/lib/cn";
import {
  domainRoutes,
  useSaveDomain,
  type DnsCheck,
  type Domain,
  type DomainInput,
  type TLSMode,
} from "@/lib/domains";
import { validHostname } from "@/lib/validate";
import { Checkbox, describedBy, FieldError, selectCls } from "./form-bits";
import {
  newRoute,
  routeDraft,
  routeFieldId,
  routeInput,
  RouteList,
  sortRoutes,
  validateRoutes,
  type RouteDraft,
  type RouteField,
} from "./route-list";
import { cfSwitchId, headerDraft, headerFieldId, SecurityTab, validateHeaders, type HeaderDraft, type HeaderField } from "./security-tab";
import { certSelectId, TlsTab } from "./tls-tab";

export const WWW_OPTIONS = [
  { value: "none", label: "Nenhum" },
  { value: "www_to_apex", label: "www → principal" },
  { value: "apex_to_www", label: "principal → www" },
] as const;

type EditorTab = "rotas" | "certificado" | "seguranca";

const ALL_TABS: { value: EditorTab; label: string }[] = [
  { value: "rotas", label: "Rotas" },
  { value: "certificado", label: "Certificado" },
  { value: "seguranca", label: "Segurança" },
];

/**
 * Chave de erro do editor:
 * - `hostname`, `wwwRedirect`, `routes`, `tls`, `cloudflareOnly`, `headers` e `form`;
 * - `r<chave>.<campo>` para uma rota e `h<chave>.<campo>` para um cabeçalho.
 */
type ErrorKey = string;

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

/**
 * Erro da API → chave do campo no editor e mensagem. `routeKeys` e
 * `headerKeys` são as chaves das rotas e cabeçalhos na ordem em que foram
 * enviados (o servidor aponta `routes[i]` e `headers[i]`).
 */
export function domainErrorMessage(
  err: unknown,
  routeKeys: number[] = [],
  headerKeys: number[] = [],
  defaultRouteKey?: number,
): { key?: ErrorKey; message: string } {
  if (!(err instanceof ApiError)) return { message: err instanceof Error ? err.message : "Erro desconhecido." };
  const f = err.field ?? "";
  switch (err.code) {
    case "DOMAIN_EXISTS":
      return { key: "hostname", message: "Este domínio já está cadastrado." };
    case "PANEL_DOMAIN_PROTECTED":
      return { message: "O hostname do painel só muda em Configurações → Painel." };
    case "PANEL_PORT_OFF":
      return { key: "cloudflareOnly", message: err.message };
  }
  const route = /^routes\[(\d+)\]\.(\w+)$/.exec(f);
  if (route && routeKeys[Number(route[1])] !== undefined) {
    return { key: `r${routeKeys[Number(route[1])]}.${route[2]}`, message: err.message };
  }
  const header = /^headers\[(\d+)\]\.(name|value)$/.exec(f);
  if (header && headerKeys[Number(header[1])] !== undefined) {
    return { key: `h${headerKeys[Number(header[1])]}.${header[2]}`, message: err.message };
  }
  if (f.startsWith("tls")) return { key: "tls", message: err.message };
  // Formato antigo (sem rotas): o campo é da rota "/".
  if ((f === "appId" || f === "address") && defaultRouteKey !== undefined) {
    return { key: `r${defaultRouteKey}.${f}`, message: err.message };
  }
  if (["hostname", "wwwRedirect", "routes", "headers", "cloudflareOnly"].includes(f)) {
    return { key: f, message: err.message };
  }
  return { message: err.message };
}

/** Aba onde fica o campo de cada chave de erro (hostname fica acima das abas). */
function tabOf(key: ErrorKey): EditorTab | null {
  if (key === "hostname") return null;
  if (key === "tls") return "certificado";
  if (key === "cloudflareOnly" || key === "headers" || key.startsWith("h")) return "seguranca";
  return "rotas";
}

/** Hostnames servidos por um domínio: ele e, com redirecionamento www, o par. */
function hostsOf(hostname: string, www: string): string[] {
  if (www === "www_to_apex") return [hostname, `www.${hostname}`];
  if (www === "apex_to_www") return [hostname, hostname.replace(/^www\./, "")];
  return [hostname];
}

/** Erros com o prefixo dado, sem o prefixo (para as abas). */
function scoped(errors: Record<ErrorKey, string | undefined>, prefix: string) {
  const out: Record<string, string | undefined> = {};
  for (const [k, v] of Object.entries(errors)) {
    if (k.startsWith(prefix) && /^\d/.test(k.slice(prefix.length))) out[k.slice(prefix.length)] = v;
  }
  return out;
}

export interface DomainDialogProps {
  /** Domínio em edição; sem ele, cria um novo. */
  domain?: Domain;
  onClose: () => void;
}

/**
 * Editor de domínio em diálogo, com as abas Rotas, Certificado e Segurança.
 * Valida no cliente antes de enviar e sempre manda o objeto inteiro (o que se
 * vê é o que se grava). No domínio do painel só há Certificado e Segurança.
 * Salvo com DNS errado, o diálogo continua aberto mostrando o registro A
 * esperado; o domínio já está gravado.
 */
export function DomainDialog({ domain, onClose }: DomainDialogProps) {
  const uid = useId();
  const id = (f: string) => `${uid}-${f}`;
  const formId = id("form");
  const tabsId = id("tabs");
  const isPanel = Boolean(domain?.isPanel);

  const apps = useApps();
  const save = useSaveDomain();

  const [tab, setTab] = useState<EditorTab>(isPanel ? "certificado" : "rotas");
  const [hostname, setHostname] = useState(domain?.hostname ?? "");
  const [routes, setRoutes] = useState<RouteDraft[]>(() =>
    sortRoutes(domain ? domainRoutes(domain).map(routeDraft) : [newRoute("/")]),
  );
  const [www, setWww] = useState(domain?.wwwRedirect || "none");
  const [forceHttps, setForceHttps] = useState(domain?.forceHttps ?? true);
  const [tlsMode, setTlsMode] = useState<TLSMode>(domain?.tls?.mode ?? "auto");
  const [certId, setCertId] = useState<number | null>(domain?.tls?.certId ?? null);
  const [cloudflareOnly, setCloudflareOnly] = useState(domain?.cloudflareOnly ?? false);
  const [headers, setHeaders] = useState<HeaderDraft[]>(() => (domain?.headers ?? []).map(headerDraft));
  const [compression, setCompression] = useState(domain?.compression ?? true);
  const [errors, setErrors] = useState<Record<ErrorKey, string | undefined>>({});
  const [formError, setFormError] = useState("");
  const [warning, setWarning] = useState<{ hosts: string[]; dns: DnsCheck } | null>(null);

  const clear = (k: ErrorKey) => setErrors((e) => (e[k] ? { ...e, [k]: undefined } : e));

  const focusKey = (k: ErrorKey, list: RouteDraft[] = routes) => {
    const t = tabOf(k);
    if (t) setTab(t);
    let el = "";
    const route = /^r(\d+)\.(\w+)$/.exec(k);
    const header = /^h(\d+)\.(name|value)$/.exec(k);
    if (k === "hostname") el = id("hostname");
    else if (k === "wwwRedirect") el = id("wwwRedirect");
    else if (k === "tls") el = certSelectId(uid);
    else if (k === "cloudflareOnly") el = cfSwitchId(uid);
    else if (k === "headers") el = `${uid}-add-header`;
    else if (k === "routes") el = list.length > 1 ? routeFieldId(uid, list[0].key, "path") : `${uid}-add-route`;
    else if (route) el = routeFieldId(uid, Number(route[1]), route[2] as RouteField);
    else if (header) el = headerFieldId(uid, Number(header[1]), header[2] as HeaderField);
    // Espera a aba renderizar.
    requestAnimationFrame(() => document.getElementById(el)?.focus());
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (save.isPending) return;
    const host = hostname.trim().toLowerCase();
    const sorted = sortRoutes(routes);
    const next: Record<ErrorKey, string> = {};
    if (!isPanel) {
      if (!validHostname(host)) {
        next.hostname = "Hostname inválido. Use letras minúsculas, números e hífens, como loja.exemplo.com.";
      }
      for (const r of validateRoutes(sorted)) next[`r${r.key}.${r.field}`] ??= r.message;
      const mismatch = !next.hostname ? wwwMismatch(host, www) : "";
      if (mismatch) next.wwwRedirect = mismatch;
    }
    if (tlsMode === "custom" && certId === null) next.tls = "Escolha o certificado próprio ou adicione um.";
    for (const h of validateHeaders(headers)) next[`h${h.key}.${h.field}`] ??= h.message;

    setRoutes(sorted);
    setErrors(next);
    setFormError("");
    const keys = Object.keys(next);
    if (keys.length > 0) {
      // O primeiro erro na ordem da tela: o da aba aberta, se houver; senão o primeiro.
      const here = keys.find((k) => tabOf(k) === tab || tabOf(k) === null);
      focusKey(here ?? keys[0], sorted);
      return;
    }

    const common = {
      tls: { mode: tlsMode, certId: tlsMode === "custom" ? certId : null },
      cloudflareOnly,
      headers: headers.map((h) => ({ name: h.name.trim(), value: h.value })),
      compression,
    };
    const input: DomainInput = isPanel
      ? { hostname: domain!.hostname, ...common }
      : { hostname: host, wwwRedirect: www, forceHttps, routes: sorted.map(routeInput), ...common };
    const routeKeys = sorted.map((r) => r.key);
    const headerKeys = headers.map((h) => h.key);
    const defaultKey = sorted.find((r) => r.isDefault)?.key;

    save.mutate(
      { id: domain?.id, input },
      {
        onSuccess: (r) => {
          if (r.dns.ok) onClose();
          else setWarning({ hosts: hostsOf(r.domain.hostname, r.domain.wwwRedirect), dns: r.dns });
        },
        onError: (err) => {
          const m = domainErrorMessage(err, routeKeys, headerKeys, defaultKey);
          if (m.key) {
            setErrors({ [m.key]: m.message });
            focusKey(m.key, sorted);
          } else {
            setFormError(m.message);
          }
        },
      },
    );
  };

  const title = isPanel ? "Editar domínio do painel" : domain ? "Editar domínio" : "Adicionar domínio";

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

  const tabs = isPanel ? ALL_TABS.filter((t) => t.value !== "rotas") : ALL_TABS;
  const routeErrors = scoped(errors, "r");
  const headerErrors = { ...scoped(errors, "h"), cloudflareOnly: errors.cloudflareOnly, headers: errors.headers };

  return (
    <Dialog
      open
      size="lg"
      eyebrow={isPanel ? "Painel KoreGO" : domain ? "Domínio" : "Novo domínio"}
      title={title}
      confirmLabel="Salvar"
      formId={formId}
      loading={save.isPending}
      onCancel={onClose}
    >
      <form id={formId} noValidate onSubmit={submit} className="space-y-4 text-fg">
        {formError && <Alert variant="danger">{formError}</Alert>}

        {isPanel ? (
          <div>
            <p className="font-mono text-[13px] text-fg [overflow-wrap:anywhere]" translate="no">
              {domain!.hostname}
            </p>
            <p className="mt-1 text-xs text-fg-subtle">
              A rota é fixa. O hostname muda em Configurações → Painel.
            </p>
          </div>
        ) : (
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
              aria-describedby={describedBy(errors.hostname && id("hostname-error"))}
              onChange={(e) => {
                setHostname(e.target.value);
                clear("hostname");
                clear("wwwRedirect");
              }}
            />
            <FieldError id={id("hostname-error")}>{errors.hostname}</FieldError>
          </div>
        )}

        <div>
          <Tabs
            id={tabsId}
            label="Seções do domínio"
            items={tabs}
            value={tab}
            onChange={setTab}
            className="flex w-full [&>button]:flex-1"
          />
          <div
            role="tabpanel"
            id={tabPanelId(tabsId)}
            aria-labelledby={tabId(tabsId, tab)}
            className="mt-4"
          >
            {tab === "rotas" && (
              <div className="space-y-4">
                <RouteList
                  base={uid}
                  routes={routes}
                  onChange={setRoutes}
                  errors={routeErrors}
                  listError={errors.routes}
                  onClearError={(key, f) => {
                    clear(`r${key}.${f}`);
                    clear("routes");
                  }}
                  apps={apps}
                />
                <div className="grid gap-4 border-t border-line pt-4 sm:grid-cols-2">
                  <div>
                    <Label htmlFor={id("wwwRedirect")}>Redirecionamento www</Label>
                    <select
                      id={id("wwwRedirect")}
                      className={cn(selectCls, "mt-1.5")}
                      value={www}
                      aria-invalid={errors.wwwRedirect ? true : undefined}
                      aria-describedby={describedBy(errors.wwwRedirect && id("wwwRedirect-error"))}
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
                    <FieldError id={id("wwwRedirect-error")}>{errors.wwwRedirect}</FieldError>
                  </div>
                  <div className="flex items-end">
                    <Checkbox checked={forceHttps} onChange={setForceHttps}>
                      Forçar HTTPS
                    </Checkbox>
                  </div>
                </div>
              </div>
            )}
            {tab === "certificado" && (
              <TlsTab
                base={uid}
                mode={tlsMode}
                certId={certId}
                onMode={setTlsMode}
                onCertId={setCertId}
                error={errors.tls}
                onClearError={() => clear("tls")}
                current={domain?.cert}
              />
            )}
            {tab === "seguranca" && (
              <SecurityTab
                base={uid}
                cloudflareOnly={cloudflareOnly}
                onCloudflareOnly={setCloudflareOnly}
                headers={headers}
                onHeaders={setHeaders}
                compression={compression}
                onCompression={setCompression}
                errors={headerErrors}
                onClearError={(k) => clear(k === "cloudflareOnly" || k === "headers" ? k : `h${k}`)}
              />
            )}
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
