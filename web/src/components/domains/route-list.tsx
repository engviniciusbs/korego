"use client";

import type { ReactNode } from "react";
import { Button, Input, Label, Tabs, tabId, tabPanelId } from "@/components/ui";
import type { AppView } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { byPrecedence, MAX_ROUTES, type DomainRoute, type RouteTarget } from "@/lib/domains";
import { validAddress, validImmutablePrefix, validRoutePath, validStaticRoot } from "@/lib/validate";
import { Checkbox, describedBy, FieldError, Hint, selectCls } from "./form-bits";

/** Rota em edição: os campos de todos os tipos ficam guardados ao trocar o tipo. */
export interface RouteDraft {
  key: number;
  /** A rota "/" do domínio: obrigatória, com caminho fixo. */
  isDefault: boolean;
  path: string;
  targetType: RouteTarget;
  appId: string;
  address: string;
  /** Limite do corpo em MB, como digitado ("" ou "0" = sem limite). */
  maxMb: string;
  noBuffering: boolean;
  root: string;
  spa: boolean;
  immutablePrefix: string;
}

export type RouteField = "path" | "appId" | "address" | "maxBodyBytes" | "root" | "immutablePrefix" | "targetType";

const MB = 1024 * 1024;

let nextKey = 1;
const newKey = () => nextKey++;

export function newRoute(path = ""): RouteDraft {
  return {
    key: newKey(),
    isDefault: path === "/",
    path,
    targetType: "app",
    appId: "",
    address: "",
    maxMb: "",
    noBuffering: false,
    root: "",
    spa: false,
    immutablePrefix: "",
  };
}

export function routeDraft(r: DomainRoute): RouteDraft {
  return {
    key: newKey(),
    isDefault: r.path === "/",
    path: r.path,
    targetType: r.targetType,
    appId: r.appId ? String(r.appId) : "",
    address: r.address ?? "",
    maxMb: r.maxBodyBytes > 0 ? String(Math.round((r.maxBodyBytes / MB) * 100) / 100) : "",
    noBuffering: r.noBuffering,
    root: r.root ?? "",
    spa: r.spa,
    immutablePrefix: r.immutablePrefix ?? "",
  };
}

const maxBytes = (mb: string) => {
  const n = Number(mb.trim() || "0");
  return Number.isFinite(n) && n > 0 ? Math.round(n * MB) : 0;
};

/** Rascunho → corpo da API, só com os campos do tipo escolhido. */
export function routeInput(d: RouteDraft): DomainRoute {
  const path = d.path.trim();
  switch (d.targetType) {
    case "app":
      return {
        path,
        targetType: "app",
        appId: Number(d.appId),
        maxBodyBytes: maxBytes(d.maxMb),
        noBuffering: d.noBuffering,
        spa: false,
      };
    case "address":
      return {
        path,
        targetType: "address",
        address: d.address.trim(),
        maxBodyBytes: maxBytes(d.maxMb),
        noBuffering: d.noBuffering,
        spa: false,
      };
    case "static": {
      const out: DomainRoute = { path, targetType: "static", root: d.root.trim(), spa: d.spa, maxBodyBytes: 0, noBuffering: false };
      const imm = d.immutablePrefix.trim();
      if (imm) out.immutablePrefix = imm;
      return out;
    }
  }
}

export const sortRoutes = (rs: RouteDraft[]) => [...rs].sort((a, b) => byPrecedence(a.path.trim(), b.path.trim()));

/** Confere as rotas como o servidor; devolve os erros por `rota.campo`. */
export function validateRoutes(rs: RouteDraft[]): { key: number; field: RouteField; message: string }[] {
  const out: { key: number; field: RouteField; message: string }[] = [];
  const seen = new Set<string>();
  for (const r of rs) {
    const p = r.path.trim();
    if (!validRoutePath(p)) {
      out.push({
        key: r.key,
        field: "path",
        message: "Caminho inválido. Use um prefixo que comece e termine com /, como /api/.",
      });
    } else if (seen.has(p)) {
      out.push({ key: r.key, field: "path", message: `O caminho ${p} aparece mais de uma vez.` });
    }
    seen.add(p);
    if (r.targetType === "app" && !r.appId) out.push({ key: r.key, field: "appId", message: "Escolha a aplicação." });
    if (r.targetType === "address" && !validAddress(r.address.trim())) {
      out.push({ key: r.key, field: "address", message: "Endereço inválido. Use host:porta, como 127.0.0.1:5000." });
    }
    if (r.targetType !== "static") {
      const n = Number(r.maxMb.trim() || "0");
      if (!Number.isFinite(n) || n < 0) {
        out.push({ key: r.key, field: "maxBodyBytes", message: "Use um número de MB maior ou igual a 0 (0 = sem limite)." });
      }
    }
    if (r.targetType === "static") {
      if (!validStaticRoot(r.root.trim())) {
        out.push({
          key: r.key,
          field: "root",
          message: "Pasta inválida. Use um caminho absoluto dentro de /var/www, como /var/www/site.",
        });
      }
      const imm = r.immutablePrefix.trim();
      if (imm && !validImmutablePrefix(imm)) {
        out.push({
          key: r.key,
          field: "immutablePrefix",
          message: "Prefixo inválido. Use um caminho que comece e termine com /, como /assets/.",
        });
      }
    }
  }
  return out;
}

const TARGETS: { value: RouteTarget; label: string }[] = [
  { value: "app", label: "Aplicação" },
  { value: "address", label: "Endereço" },
  { value: "static", label: "Pasta" },
];

export interface RouteListProps {
  /** Base dos ids dos campos (`${base}-r${key}-${campo}`). */
  base: string;
  routes: RouteDraft[];
  onChange: (routes: RouteDraft[]) => void;
  /** Erro de cada campo, por `${key}.${campo}`, e o erro geral da lista. */
  errors: Record<string, string | undefined>;
  listError?: string;
  onClearError: (key: number, field: RouteField) => void;
  apps: { data?: AppView[]; isPending: boolean; isSuccess: boolean; isError: boolean; error: Error | null };
}

export const routeFieldId = (base: string, key: number, f: RouteField) => `${base}-r${key}-${f}`;
const errorId = (base: string, key: number, f: RouteField) => `${routeFieldId(base, key, f)}-error`;

/**
 * Rotas do domínio, na ordem em que o proxy as confere: o caminho mais longo
 * primeiro e "/" por último. Com só a rota "/", é o formulário de destino de
 * sempre; "Adicionar rota por caminho" abre a lista.
 */
export function RouteList({ base, routes, onChange, errors, listError, onClearError, apps }: RouteListProps) {
  const update = (key: number, patch: Partial<RouteDraft>) =>
    onChange(routes.map((r) => (r.key === key ? { ...r, ...patch } : r)));

  const add = () => {
    if (routes.length >= MAX_ROUTES) return;
    const r = newRoute();
    onChange(sortRoutes([...routes, r]));
    requestAnimationFrame(() => document.getElementById(routeFieldId(base, r.key, "path"))?.focus());
  };

  const remove = (key: number) => {
    onChange(routes.filter((r) => r.key !== key));
    // O foco vai para "Adicionar rota", que continua no lugar.
    requestAnimationFrame(() => document.getElementById(`${base}-add-route`)?.focus());
  };

  const fieldsOf = (r: RouteDraft) => (
    <RouteFields
      base={base}
      route={r}
      errors={errors}
      apps={apps}
      onChange={(patch) => update(r.key, patch)}
      onClearError={(f) => onClearError(r.key, f)}
    />
  );

  if (routes.length === 1 && routes[0].isDefault) {
    const r = routes[0];
    return (
      <div className="space-y-2">
        <fieldset>
          <legend className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted">Destino</legend>
          {fieldsOf(r)}
        </fieldset>
        <Button id={`${base}-add-route`} size="sm" variant="ghost" onClick={add} className="-ml-2">
          Adicionar rota por caminho
        </Button>
      </div>
    );
  }

  const full = routes.length >= MAX_ROUTES;
  return (
    <div>
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <p className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted">
            Rotas <span className="text-fg-subtle tabular-nums">{routes.length}/{MAX_ROUTES}</span>
          </p>
          <Hint id={`${base}-routes-hint`} className="mt-0.5">
            Na ordem em que o proxy confere: o caminho mais longo vence, e / atende o resto.
          </Hint>
        </div>
        <Button
          id={`${base}-add-route`}
          size="sm"
          onClick={add}
          disabled={full}
          aria-describedby={full ? `${base}-routes-full` : undefined}
        >
          Adicionar rota
        </Button>
      </div>
      {full && (
        <Hint id={`${base}-routes-full`}>Limite de {MAX_ROUTES} rotas por domínio.</Hint>
      )}
      {listError && (
        <p id={`${base}-routes-error`} role="alert" className="mt-2 text-xs text-danger">
          {listError}
        </p>
      )}
      <ol className="mt-3 space-y-2" aria-describedby={`${base}-routes-hint`}>
        {routes.map((r) => {
          const isRoot = r.isDefault;
          const label = r.path.trim() || "nova";
          return (
            <li key={r.key}>
              <div
                role="group"
                aria-label={`Rota ${label}`}
                className={cn(
                  "border border-line bg-bg/30",
                  isRoot && "shadow-[inset_2px_0_0_0_var(--color-line-strong)]",
                )}
              >
                <div className="flex items-start gap-2 border-b border-line px-3 py-2">
                  {isRoot ? (
                    <p className="flex min-h-7 flex-1 flex-wrap items-baseline gap-x-2 text-xs text-fg-subtle">
                      <span className="font-mono text-[13px] text-fg" translate="no">
                        /
                      </span>
                      <span>padrão: o que nenhuma outra rota atende</span>
                    </p>
                  ) : (
                    <div className="min-w-0 flex-1">
                      <PathInput
                        base={base}
                        route={r}
                        error={errors[`${r.key}.path`]}
                        onChange={(path) => {
                          update(r.key, { path });
                          onClearError(r.key, "path");
                        }}
                        onCommit={() => onChange(sortRoutes(routes))}
                      />
                    </div>
                  )}
                  {!isRoot && (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="shrink-0 enabled:hover:bg-danger-bg enabled:hover:text-danger"
                      onClick={() => remove(r.key)}
                    >
                      Remover<span className="sr-only"> a rota {label}</span>
                    </Button>
                  )}
                </div>
                <div className="px-3 pt-2 pb-3">{fieldsOf(r)}</div>
              </div>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

function PathInput({
  base,
  route,
  error,
  onChange,
  onCommit,
}: {
  base: string;
  route: RouteDraft;
  error?: string;
  onChange: (path: string) => void;
  onCommit: () => void;
}) {
  const id = routeFieldId(base, route.key, "path");
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <Label htmlFor={id} className="sr-only">
        Caminho
      </Label>
      <Input
        id={id}
        mono
        size="sm"
        value={route.path}
        placeholder="/api/"
        autoComplete="off"
        spellCheck={false}
        autoCapitalize="none"
        invalid={Boolean(error)}
        aria-describedby={describedBy(error && errorId(base, route.key, "path"))}
        className="max-w-60"
        onChange={(e) => onChange(e.target.value)}
        onBlur={onCommit}
      />
      <div className="w-full">
        <FieldError id={errorId(base, route.key, "path")}>{error}</FieldError>
      </div>
    </div>
  );
}

function RouteFields({
  base,
  route: r,
  errors,
  apps,
  onChange,
  onClearError,
}: {
  base: string;
  route: RouteDraft;
  errors: Record<string, string | undefined>;
  apps: RouteListProps["apps"];
  onChange: (patch: Partial<RouteDraft>) => void;
  onClearError: (f: RouteField) => void;
}) {
  const id = (f: RouteField) => routeFieldId(base, r.key, f);
  const err = (f: RouteField) => errors[`${r.key}.${f}`];
  const desc = (f: RouteField, ...hints: (string | false | undefined)[]) =>
    describedBy(err(f) && errorId(base, r.key, f), ...hints);
  const tabsId = `${base}-r${r.key}-target`;
  const appList = apps.data ?? [];

  const set = (f: RouteField, patch: Partial<RouteDraft>) => {
    onChange(patch);
    onClearError(f);
  };

  let body: ReactNode;
  if (r.targetType === "app") {
    body = (
      <div>
        <Label htmlFor={id("appId")} required>
          Aplicação
        </Label>
        <select
          id={id("appId")}
          className={cn(selectCls, "mt-1.5 font-mono")}
          value={r.appId}
          disabled={apps.isPending}
          aria-invalid={err("appId") ? true : undefined}
          aria-describedby={desc("appId", apps.isSuccess && appList.length === 0 && `${id("appId")}-hint`)}
          onChange={(e) => set("appId", { appId: e.target.value })}
        >
          <option value="">{apps.isPending ? "Carregando…" : "Selecione…"}</option>
          {appList.map((a) => (
            <option key={a.id} value={a.id}>
              {a.slug}
            </option>
          ))}
        </select>
        <FieldError id={errorId(base, r.key, "appId")}>{err("appId")}</FieldError>
        {apps.isError && <Hint>Não foi possível listar as aplicações: {apps.error?.message}</Hint>}
        {apps.isSuccess && appList.length === 0 && (
          <Hint id={`${id("appId")}-hint`}>Nenhuma aplicação cadastrada. Use um endereço host:porta.</Hint>
        )}
      </div>
    );
  } else if (r.targetType === "address") {
    body = (
      <div className="space-y-3">
        <div>
          <Label htmlFor={id("address")} required>
            Endereço (host:porta)
          </Label>
          <Input
            id={id("address")}
            mono
            className="mt-1.5"
            value={r.address}
            placeholder="127.0.0.1:5000"
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="none"
            invalid={Boolean(err("address"))}
            aria-describedby={desc("address", `${id("address")}-hint`)}
            onChange={(e) => set("address", { address: e.target.value })}
          />
          <FieldError id={errorId(base, r.key, "address")}>{err("address")}</FieldError>
          <Hint id={`${id("address")}-hint`}>Um serviço que já roda no servidor, fora do KoreGO.</Hint>
        </div>
        <div className="grid gap-x-4 gap-y-2 sm:grid-cols-2">
          <div>
            <Label htmlFor={id("maxBodyBytes")}>Limite do corpo (MB)</Label>
            <Input
              id={id("maxBodyBytes")}
              type="number"
              min={0}
              step="any"
              inputMode="decimal"
              mono
              className="mt-1.5"
              value={r.maxMb}
              placeholder="0"
              invalid={Boolean(err("maxBodyBytes"))}
              aria-describedby={desc("maxBodyBytes", `${id("maxBodyBytes")}-hint`)}
              onChange={(e) => set("maxBodyBytes", { maxMb: e.target.value })}
            />
            <FieldError id={errorId(base, r.key, "maxBodyBytes")}>{err("maxBodyBytes")}</FieldError>
            <Hint id={`${id("maxBodyBytes")}-hint`}>0 = sem limite. Acima dele, a resposta é 413.</Hint>
          </div>
          <div className="sm:pt-5">
            <Checkbox
              checked={r.noBuffering}
              onChange={(v) => onChange({ noBuffering: v })}
              describedBy={`${base}-r${r.key}-nobuf-hint`}
            >
              Sem buffer
            </Checkbox>
            <Hint id={`${base}-r${r.key}-nobuf-hint`} className="mt-0">
              Repassa a resposta na hora, para streaming e eventos.
            </Hint>
          </div>
        </div>
      </div>
    );
  } else {
    body = (
      <div className="space-y-3">
        <div>
          <Label htmlFor={id("root")} required>
            Pasta
          </Label>
          <Input
            id={id("root")}
            mono
            className="mt-1.5"
            value={r.root}
            placeholder="/var/www/site"
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="none"
            invalid={Boolean(err("root"))}
            aria-describedby={desc("root", `${id("root")}-hint`)}
            onChange={(e) => set("root", { root: e.target.value })}
          />
          <FieldError id={errorId(base, r.key, "root")}>{err("root")}</FieldError>
          <Hint id={`${id("root")}-hint`}>Arquivos servidos direto, de dentro de /var/www.</Hint>
        </div>
        <div className="grid gap-x-4 gap-y-2 sm:grid-cols-2">
          <div className="sm:pt-5">
            <Checkbox
              checked={r.spa}
              onChange={(v) => onChange({ spa: v })}
              describedBy={`${base}-r${r.key}-spa-hint`}
            >
              SPA
            </Checkbox>
            <Hint id={`${base}-r${r.key}-spa-hint`} className="mt-0">
              Caminho desconhecido recebe o /index.html.
            </Hint>
          </div>
          <div>
            <Label htmlFor={id("immutablePrefix")}>Cache imutável</Label>
            <Input
              id={id("immutablePrefix")}
              mono
              className="mt-1.5"
              value={r.immutablePrefix}
              placeholder="/assets/"
              autoComplete="off"
              spellCheck={false}
              autoCapitalize="none"
              invalid={Boolean(err("immutablePrefix"))}
              aria-describedby={desc("immutablePrefix", `${id("immutablePrefix")}-hint`)}
              onChange={(e) => set("immutablePrefix", { immutablePrefix: e.target.value })}
            />
            <FieldError id={errorId(base, r.key, "immutablePrefix")}>{err("immutablePrefix")}</FieldError>
            <Hint id={`${id("immutablePrefix")}-hint`}>Opcional. Arquivos com hash no nome, guardados por 1 ano.</Hint>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div>
      <Tabs
        id={tabsId}
        label="Tipo de destino"
        items={TARGETS}
        value={r.targetType}
        onChange={(t) => {
          onChange({ targetType: t });
          onClearError("targetType");
        }}
        className="mt-1.5"
      />
      <div role="tabpanel" id={tabPanelId(tabsId)} aria-labelledby={tabId(tabsId, r.targetType)} className="mt-3">
        {body}
      </div>
    </div>
  );
}
