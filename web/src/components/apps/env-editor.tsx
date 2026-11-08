"use client";

import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Input, Panel, Spinner } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { useAppEnv, useSaveEnv } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { validEnvName } from "@/lib/validate";

interface NewRow {
  key: number;
  name: string;
  value: string;
}

/** Cópia do objeto sem a chave `k`. */
function without(o: Record<string, string>, k: string): Record<string, string> {
  const next = { ...o };
  delete next[k];
  return next;
}

/** Chave de erro por linha: `e:<nome>` (existente) ou `n:<key>` (nova). */
type RowKey = string;

const th = "px-3 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const td = "px-3 py-2 align-top";

const Masked = () => (
  <>
    <span aria-hidden="true" className="font-mono text-xs text-fg-subtle">
      ••••••
    </span>
    <span className="sr-only">valor oculto</span>
  </>
);

/**
 * Editor das variáveis de ambiente da aplicação. Só os nomes vêm do servidor;
 * valores são digitados em campos de senha e enviados apenas quando mudam.
 * Nenhum valor existente é exibido nem preenchido.
 */
export function EnvEditor({ appId }: { appId: number }) {
  const uid = useId();
  const env = useAppEnv(appId);
  const save = useSaveEnv(appId);
  const nextKey = useRef(1);

  const [added, setAdded] = useState<NewRow[]>([]);
  const [replacing, setReplacing] = useState<Record<string, string>>({});
  const [removing, setRemoving] = useState<string[]>([]);
  const [errors, setErrors] = useState<Record<RowKey, string>>({});
  const [formError, setFormError] = useState("");
  const [savedAt, setSavedAt] = useState(false);

  const names = env.data ?? [];
  // Substituição ainda vazia não é mudança: ela é ignorada ao salvar e o valor antigo fica.
  const replaced = Object.fromEntries(Object.entries(replacing).filter(([, v]) => v !== ""));
  const dirty = added.length > 0 || Object.keys(replaced).length > 0 || removing.length > 0;
  const errId = (k: RowKey) => `${uid}-${k}-error`;

  // Avisa antes de sair da página com alterações não salvas.
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const touch = (k?: RowKey) => {
    setSavedAt(false);
    if (k) setErrors((e) => (e[k] ? { ...e, [k]: "" } : e));
  };

  const startReplace = (n: string) => {
    setReplacing((r) => ({ ...r, [n]: "" }));
    setRemoving((r) => r.filter((x) => x !== n));
    touch(`e:${n}`);
    requestAnimationFrame(() => document.getElementById(`${uid}-e-${n}`)?.focus());
  };
  const cancelReplace = (n: string) => {
    setReplacing((r) => without(r, n));
    touch(`e:${n}`);
  };
  const toggleRemove = (n: string) => {
    setRemoving((r) => (r.includes(n) ? r.filter((x) => x !== n) : [...r, n]));
    setReplacing((r) => without(r, n));
    touch(`e:${n}`);
  };
  const addRow = () => {
    const key = nextKey.current++;
    setAdded((a) => [...a, { key, name: "", value: "" }]);
    touch();
    requestAnimationFrame(() => document.getElementById(`${uid}-n-${key}-name`)?.focus());
  };
  const editRow = (key: number, patch: Partial<NewRow>) => {
    setAdded((a) => a.map((r) => (r.key === key ? { ...r, ...patch } : r)));
    touch(`n:${key}`);
  };
  const dropRow = (key: number) => {
    setAdded((a) => a.filter((r) => r.key !== key));
    touch(`n:${key}`);
  };
  const discard = () => {
    setAdded([]);
    setReplacing({});
    setRemoving([]);
    setErrors({});
    setFormError("");
  };

  const focusRow = (k: RowKey) => {
    const target = k.startsWith("n:") ? `${uid}-n-${k.slice(2)}-name` : `${uid}-e-${k.slice(2)}`;
    requestAnimationFrame(() => document.getElementById(target)?.focus());
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!dirty || save.isPending) return;
    const next: Record<RowKey, string> = {};
    const seen = new Set<string>();
    for (const r of added) {
      const n = r.name.trim();
      const k = `n:${r.key}`;
      if (!n) next[k] = "Informe o nome da variável.";
      else if (!validEnvName(n)) next[k] = "Nome inválido: use letras, números e _, começando com letra ou _.";
      else if (names.includes(n)) next[k] = `A variável ${n} já existe; use "Substituir valor".`;
      else if (seen.has(n)) next[k] = "Nome repetido nesta lista.";
      seen.add(n);
    }
    setErrors(next);
    setFormError("");
    const first = Object.keys(next)[0];
    if (first) {
      focusRow(first);
      return;
    }
    const set: Record<string, string> = { ...replaced };
    for (const r of added) set[r.name.trim()] = r.value;
    save.mutate(
      { set, remove: removing },
      {
        onSuccess: () => {
          discard();
          setSavedAt(true);
        },
        onError: (err) => {
          if (err instanceof ApiError && err.code === "INVALID_ENV" && err.field) {
            const row = added.find((r) => r.name.trim() === err.field);
            const k = row ? `n:${row.key}` : names.includes(err.field) ? `e:${err.field}` : "";
            if (k) {
              setErrors({ [k]: err.message });
              focusRow(k);
              return;
            }
          }
          setFormError(err.message);
        },
      },
    );
  };

  return (
    <Panel
      title="Variáveis de ambiente"
      description="Criptografadas no servidor. Os valores nunca são exibidos; valem a partir do próximo deploy."
      size="sm"
      className="[&>div]:p-0"
      actions={
        dirty ? (
          <Badge size="sm" variant="warning" dot>
            Alterações não salvas
          </Badge>
        ) : undefined
      }
    >
      {env.isPending ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando variáveis…</span>
        </PanelState>
      ) : env.isError ? (
        <div className="p-3">
          <Alert variant="danger" title="Não foi possível listar as variáveis.">
            <p>{env.error.message}</p>
            <Button size="sm" className="mt-2" onClick={() => void env.refetch()}>
              Tentar novamente
            </Button>
          </Alert>
        </div>
      ) : (
        <form noValidate onSubmit={submit} autoComplete="off">
          {formError && (
            <div className="p-3 pb-0">
              <Alert variant="danger">{formError}</Alert>
            </div>
          )}
          {names.length === 0 && added.length === 0 ? (
            <PanelState>
              <span>Nenhuma variável definida.</span>
            </PanelState>
          ) : (
            <div className="relative overflow-x-auto">
              <table className="w-full min-w-[560px]">
                <caption className="sr-only">Variáveis de ambiente</caption>
                <thead>
                  <tr>
                    <th scope="col" className={cn(th, "w-2/5")}>Nome</th>
                    <th scope="col" className={th}>Valor</th>
                    <th scope="col" className={cn(th, "text-right")}>
                      <span className="sr-only">Ações</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {names.map((n) => {
                    const k = `e:${n}`;
                    const isRemoving = removing.includes(n);
                    const isReplacing = n in replacing;
                    return (
                      <tr key={n} className={cn("border-t border-line", isRemoving && "bg-danger-bg/40")}>
                        <td className={cn(td, "pt-2.5")}>
                          <span
                            translate="no"
                            className={cn(
                              "font-mono text-xs [overflow-wrap:anywhere]",
                              isRemoving ? "text-fg-subtle line-through" : "text-fg",
                            )}
                          >
                            {n}
                          </span>
                          {isRemoving && <span className="ml-2 text-[11px] text-danger">será removida</span>}
                        </td>
                        <td className={td}>
                          {isReplacing ? (
                            <>
                              <Input
                                id={`${uid}-e-${n}`}
                                type="password"
                                size="sm"
                                mono
                                autoComplete="off"
                                name={`env-${n}`}
                                aria-label={`Novo valor de ${n}`}
                                placeholder="Novo valor…"
                                value={replacing[n]}
                                invalid={Boolean(errors[k])}
                                aria-describedby={errors[k] ? errId(k) : replacing[n] === "" ? `${uid}-e-${n}-hint` : undefined}
                                onChange={(e) => {
                                  const v = e.target.value;
                                  setReplacing((r) => ({ ...r, [n]: v }));
                                  touch(k);
                                }}
                              />
                              {errors[k] ? (
                                <p id={errId(k)} className="mt-1 text-xs text-danger">
                                  {errors[k]}
                                </p>
                              ) : (
                                replacing[n] === "" && (
                                  <p id={`${uid}-e-${n}-hint`} className="mt-1 text-xs text-fg-subtle">
                                    Digite o novo valor ou cancele.
                                  </p>
                                )
                              )}
                            </>
                          ) : (
                            <span className="inline-flex h-7 items-center">
                              <Masked />
                            </span>
                          )}
                        </td>
                        <td className={cn(td, "text-right")}>
                          <div className="flex items-center justify-end gap-1.5">
                            {!isRemoving &&
                              (isReplacing ? (
                                <Button size="sm" variant="ghost" onClick={() => cancelReplace(n)}>
                                  Manter valor<span className="sr-only"> de {n}</span>
                                </Button>
                              ) : (
                                <Button size="sm" onClick={() => startReplace(n)}>
                                  Substituir valor<span className="sr-only"> de {n}</span>
                                </Button>
                              ))}
                            <Button
                              size="sm"
                              variant="ghost"
                              className={isRemoving ? undefined : "enabled:hover:bg-danger-bg enabled:hover:text-danger"}
                              onClick={() => toggleRemove(n)}
                            >
                              {isRemoving ? "Desfazer remoção" : "Remover"}
                              <span className="sr-only"> da variável {n}</span>
                            </Button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                  {added.map((r, i) => {
                    const k = `n:${r.key}`;
                    const label = `nova variável ${i + 1}`;
                    return (
                      <tr key={r.key} className="border-t border-line bg-accent/5">
                        <td className={td}>
                          <Input
                            id={`${uid}-n-${r.key}-name`}
                            size="sm"
                            mono
                            autoComplete="off"
                            spellCheck={false}
                            autoCapitalize="characters"
                            aria-label={`Nome da ${label}`}
                            name={`env-new-${r.key}-name`}
                            placeholder="NOME_DA_VARIAVEL…"
                            value={r.name}
                            invalid={Boolean(errors[k])}
                            aria-describedby={errors[k] ? errId(k) : undefined}
                            onChange={(e) => editRow(r.key, { name: e.target.value })}
                          />
                          {errors[k] && (
                            <p id={errId(k)} className="mt-1 text-xs text-danger [overflow-wrap:anywhere]">
                              {errors[k]}
                            </p>
                          )}
                        </td>
                        <td className={td}>
                          <Input
                            type="password"
                            size="sm"
                            mono
                            autoComplete="off"
                            name={`env-new-${r.key}-value`}
                            aria-label={`Valor da ${label}`}
                            placeholder="Valor…"
                            value={r.value}
                            onChange={(e) => editRow(r.key, { value: e.target.value })}
                          />
                        </td>
                        <td className={cn(td, "text-right")}>
                          <Button size="sm" variant="ghost" onClick={() => dropRow(r.key)}>
                            Descartar<span className="sr-only"> {label}</span>
                          </Button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
          <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-3 py-2.5">
            <Button size="sm" onClick={addRow}>
              <span aria-hidden="true">+</span> Adicionar variável
            </Button>
            <div className="flex items-center gap-2">
              {savedAt && !dirty && (
                <span role="status" className="text-xs text-success">
                  Variáveis salvas.
                </span>
              )}
              {dirty && (
                <Button size="sm" variant="ghost" disabled={save.isPending} onClick={discard}>
                  Descartar alterações
                </Button>
              )}
              <Button type="submit" size="sm" variant="primary" disabled={!dirty} loading={save.isPending}>
                Salvar variáveis
              </Button>
            </div>
          </div>
        </form>
      )}
    </Panel>
  );
}
