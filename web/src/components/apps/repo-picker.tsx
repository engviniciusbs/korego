"use client";

import { useState } from "react";
import { Alert, Button, Input, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { cn } from "@/lib/cn";
import { useGitHubRepos, type GitHubRepo } from "@/lib/github";

/** Acima disso, só os primeiros aparecem: a busca refina. */
const MAX_SHOWN = 50;

export interface RepoPickerProps {
  /** Base dos ids (o campo de busca usa `${id}-search`). */
  id: string;
  /** Repositório escolhido (dono/nome). */
  value: string;
  onSelect: (repo: GitHubRepo) => void;
  invalid?: boolean;
  describedBy?: string;
}

/** Lista pesquisável dos repositórios das instalações do GitHub App. */
export function RepoPicker({ id, value, onSelect, invalid = false, describedBy }: RepoPickerProps) {
  const repos = useGitHubRepos();
  const [q, setQ] = useState("");

  if (repos.isPending) {
    return (
      <div className="flex h-24 items-center justify-center gap-2 border border-line text-[13px] text-fg-muted">
        <Spinner size="sm" decorative />
        <span role="status">Carregando repositórios…</span>
      </div>
    );
  }
  if (repos.isError) {
    return (
      <Alert variant="danger" title="Não foi possível listar os repositórios.">
        <p>{repos.error.message}</p>
        <Button size="sm" className="mt-2" loading={repos.isFetching} onClick={() => void repos.refetch()}>
          Tentar novamente
        </Button>
      </Alert>
    );
  }

  const all = repos.data;
  const term = q.trim().toLowerCase();
  const matched = term ? all.filter((r) => r.fullName.toLowerCase().includes(term)) : all;
  const list = matched.slice(0, MAX_SHOWN);

  return (
    <div className={cn("border", invalid ? "border-danger" : "border-line-strong")}>
      <div className="border-b border-line p-2">
        <label htmlFor={`${id}-search`} className="sr-only">
          Buscar repositório
        </label>
        <Input
          id={`${id}-search`}
          name="repo-search"
          type="search"
          size="sm"
          mono
          value={q}
          placeholder="Buscar repositório…"
          autoComplete="off"
          spellCheck={false}
          aria-describedby={describedBy}
          onChange={(e) => setQ(e.target.value)}
        />
      </div>
      {all.length === 0 ? (
        <p className="p-3 text-xs text-fg-subtle">
          Nenhum repositório disponível. Dê acesso ao App em mais repositórios no GitHub.
        </p>
      ) : list.length === 0 ? (
        <p className="p-3 text-xs text-fg-subtle" role="status">
          Nenhum repositório corresponde à busca.
        </p>
      ) : (
        <>
          <ul aria-label="Repositórios" className="max-h-56 overflow-y-auto overscroll-contain" translate="no">
            {list.map((r) => {
              const selected = r.fullName === value;
              return (
                <li key={`${r.installationId}-${r.fullName}`} className="border-t border-line first:border-t-0">
                  <button
                    type="button"
                    aria-pressed={selected}
                    onClick={() => onSelect(r)}
                    className={cn(
                      "flex w-full min-w-0 items-center justify-between gap-3 px-3 py-2 text-left font-mono text-xs",
                      "motion-safe:transition-colors motion-safe:duration-100",
                      focusRing,
                      "focus-visible:outline-offset-[-2px]",
                      selected
                        ? "bg-accent/15 text-fg shadow-[inset_2px_0_0_0_var(--color-accent)]"
                        : "text-fg-muted hover:bg-surface-2 hover:text-fg",
                    )}
                  >
                    <span className="min-w-0 [overflow-wrap:anywhere]">{r.fullName}</span>
                    <span className="shrink-0 text-[10px] text-fg-subtle">{r.defaultBranch}</span>
                  </button>
                </li>
              );
            })}
          </ul>
          {matched.length > list.length && (
            <p className="border-t border-line px-3 py-2 text-[11px] text-fg-subtle" role="status">
              Mostrando <span className="tabular-nums">{list.length}</span> de{" "}
              <span className="tabular-nums">{matched.length}</span>. Digite para refinar a busca.
            </p>
          )}
        </>
      )}
    </div>
  );
}
