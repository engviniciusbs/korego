"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { CODE_FORMAT_ERROR, CodeField, isCodeError } from "@/components/apps/code-field";
import { Checkbox, FieldError, Hint, linkCls, selectCls } from "@/components/apps/form-bits";
import { NoGitHubMessage, appHref } from "@/components/apps/page-bits";
import { RepoPicker } from "@/components/apps/repo-picker";
import { Alert, Dialog, Input, Label, Spinner } from "@/components/ui";
import { suggestSlug, useAdoptCompose, useAdoptStatic, type AdoptStaticInput } from "@/lib/adopt";
import { ApiError } from "@/lib/api";
import { STATIC_DEFAULTS, appInputOf, useSaveApp, type AppView, type UpdateMode } from "@/lib/apps";
import { cn } from "@/lib/cn";
import { useGitHub } from "@/lib/github";
import { useProjects, useSaveProject } from "@/lib/projects";
import type { DetectedSite } from "@/lib/sites";
import {
  cleanAuthCode,
  validAuthCode,
  validBranch,
  validGitUrl,
  validImageRef,
  validRelPath,
  validRepo,
  validSlug,
} from "@/lib/validate";

const NEW_PROJECT = "novo";
const SLUG_ERROR = "Nome inválido: use só minúsculas, números e hífen, começando com letra ou número.";
const REL_ERROR = "Caminho inválido. Use um caminho relativo dentro do repositório.";

type Errors = Partial<Record<string, ReactNode>>;

/** Mensagem de erro da adoção: campo (quando houver) ou geral. */
function adoptError(err: unknown): { field?: string; message: ReactNode } {
  if (!(err instanceof ApiError)) return { message: err instanceof Error ? err.message : "Erro desconhecido." };
  switch (err.code) {
    case "INVALID_APP":
      return { field: err.field, message: err.message };
    case "APP_EXISTS":
      return { field: "slug", message: "Já existe uma aplicação com esse nome." };
    case "NO_GITHUB":
      return { message: <NoGitHubMessage /> };
    default:
      return { message: err.message };
  }
}

// --- Projeto --------------------------------------------------------------------

interface ProjectChoice {
  /** id do projeto, ou NEW_PROJECT. */
  value: string;
  name: string;
}

/** Projeto existente ou "Novo projeto" com nome; cria o novo antes da adoção. */
function useProjectChoice() {
  const projects = useProjects();
  const saveProject = useSaveProject();
  const [choice, setChoice] = useState<ProjectChoice | null>(null);
  const list = [...(projects.data ?? [])].sort((a, b) => a.name.localeCompare(b.name, "pt-BR"));
  const current: ProjectChoice = choice ?? {
    value: list[0] ? String(list[0].id) : NEW_PROJECT,
    name: "",
  };

  /** Valida; devolve a mensagem de erro, se houver. */
  const check = (): string | undefined => {
    if (current.value !== NEW_PROJECT) return undefined;
    const n = current.name.trim();
    if (!n) return "Informe o nome do projeto.";
    if ([...n].length > 100) return "O nome deve ter até 100 caracteres.";
    return undefined;
  };

  /** id do projeto escolhido, criando o novo. Um projeto criado fica escolhido (repetir não o recria). */
  const resolve = async (): Promise<number> => {
    if (current.value !== NEW_PROJECT) return Number(current.value);
    const p = await saveProject.mutateAsync({ input: { name: current.name.trim(), description: "" } });
    setChoice({ value: String(p.id), name: "" });
    return p.id;
  };

  return { projects, list, current, setChoice, check, resolve, pending: saveProject.isPending };
}

function ProjectField({
  uid,
  pc,
  error,
  onEdit,
}: {
  uid: string;
  pc: ReturnType<typeof useProjectChoice>;
  error?: ReactNode;
  onEdit: () => void;
}) {
  if (pc.projects.isPending) {
    return (
      <div className="flex h-9 items-center gap-2 text-[13px] text-fg-muted">
        <Spinner size="sm" decorative />
        <span role="status">Carregando projetos…</span>
      </div>
    );
  }
  const isNew = pc.current.value === NEW_PROJECT;
  return (
    <div className="space-y-3">
      <div>
        <Label htmlFor={`${uid}-project`} required>
          Projeto
        </Label>
        <select
          id={`${uid}-project`}
          name="project"
          className={cn(selectCls, "mt-1.5")}
          value={pc.current.value}
          onChange={(e) => {
            pc.setChoice({ value: e.target.value, name: pc.current.name });
            onEdit();
          }}
        >
          {pc.list.map((p) => (
            <option key={p.id} value={String(p.id)}>
              {p.name}
            </option>
          ))}
          <option value={NEW_PROJECT}>Novo projeto…</option>
        </select>
      </div>
      {isNew && (
        <div>
          <Label htmlFor={`${uid}-projectName`} required>
            Nome do projeto
          </Label>
          <Input
            id={`${uid}-projectName`}
            name="projectName"
            className="mt-1.5"
            value={pc.current.name}
            maxLength={100}
            placeholder="Sites…"
            autoComplete="off"
            invalid={Boolean(error)}
            aria-describedby={error ? `${uid}-projectName-error` : undefined}
            onChange={(e) => {
              pc.setChoice({ value: NEW_PROJECT, name: e.target.value });
              onEdit();
            }}
          />
          <FieldError id={`${uid}-projectName-error`}>{error}</FieldError>
        </div>
      )}
    </div>
  );
}

/** Erro de criar o projeto novo, no campo do nome. */
function projectError(err: unknown): string | undefined {
  if (err instanceof ApiError && err.code === "PROJECT_EXISTS") return "Já existe um projeto com esse nome.";
  if (err instanceof ApiError && err.code === "INVALID_PROJECT") return err.message;
  return undefined;
}

// --- Bits -------------------------------------------------------------------------

function RadioCards<T extends string>({
  name,
  legend,
  value,
  options,
  onChange,
}: {
  name: string;
  legend: string;
  value: T;
  options: { value: T; label: string; hint: string }[];
  onChange: (v: T) => void;
}) {
  return (
    <fieldset>
      <legend className="mb-1.5 font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted">
        {legend}
      </legend>
      <div className="grid gap-px border border-line-strong bg-line-strong sm:grid-cols-2">
        {options.map((o) => {
          const checked = value === o.value;
          return (
            <label
              key={o.value}
              className={cn(
                "relative flex cursor-pointer flex-col gap-0.5 px-3 py-2.5 motion-safe:transition-colors motion-safe:duration-100",
                "has-[:focus-visible]:outline-solid has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-[-2px] has-[:focus-visible]:outline-accent-text",
                checked ? "bg-surface-2 shadow-[inset_2px_0_0_0_var(--color-accent)]" : "bg-surface hover:bg-surface-2",
              )}
            >
              <input
                type="radio"
                name={name}
                value={o.value}
                checked={checked}
                onChange={() => onChange(o.value)}
                className="sr-only"
              />
              <span className={cn("text-[13px] font-medium", checked ? "text-fg" : "text-fg-muted")}>{o.label}</span>
              <span className="text-[11px] text-fg-subtle">{o.hint}</span>
            </label>
          );
        })}
      </div>
    </fieldset>
  );
}

function TextField({
  id,
  label,
  value,
  onChange,
  error,
  hint,
  required,
  placeholder,
  inputMode,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  error?: ReactNode;
  hint?: ReactNode;
  required?: boolean;
  placeholder?: string;
  inputMode?: "url" | "text";
}) {
  const describe = [error ? `${id}-error` : "", hint ? `${id}-hint` : ""].filter(Boolean).join(" ") || undefined;
  return (
    <div>
      <Label htmlFor={id} required={required}>
        {label}
      </Label>
      <Input
        id={id}
        name={id}
        mono
        className="mt-1.5"
        value={value}
        placeholder={placeholder}
        inputMode={inputMode}
        autoComplete="off"
        spellCheck={false}
        autoCapitalize="none"
        invalid={Boolean(error)}
        aria-describedby={describe}
        onChange={(e) => onChange(e.target.value)}
      />
      <FieldError id={`${id}-error`}>{error}</FieldError>
      {hint && <Hint id={`${id}-hint`}>{hint}</Hint>}
    </div>
  );
}

const focusFirst = (uid: string, order: string[], errors: Errors) => {
  const first = order.find((f) => errors[f]);
  if (first) {
    // O seletor de repositórios tem o foco no campo de busca.
    const el = () => document.getElementById(`${uid}-${first}`) ?? document.getElementById(`${uid}-${first}-search`);
    requestAnimationFrame(() => el()?.focus());
  }
  return Boolean(first);
};

// --- Estático ---------------------------------------------------------------------

type GitSource = "github" | "git";

const SOURCES: { value: GitSource; label: string; hint: string }[] = [
  { value: "github", label: "GitHub", hint: "Repositório do GitHub App" },
  { value: "git", label: "URL Git pública", hint: "Clone https sem credenciais" },
];

interface StaticForm {
  slug: string;
  sourceType: GitSource;
  repo: string;
  branch: string;
  staticBuildCmd: string;
  staticOutputDir: string;
  staticBaseDir: string;
  staticImage: string;
  staticImmutablePrefix: string;
  staticSpa: boolean;
}

const STATIC_ORDER = ["projectName", "slug", "repo", "branch", "staticOutputDir", "staticBaseDir", "staticImage", "staticImmutablePrefix"];

function StaticAdopt({ site, onClose }: { site: DetectedSite; onClose: () => void }) {
  const uid = useId();
  const formId = `${uid}-form`;
  const router = useRouter();
  const github = useGitHub();
  const pc = useProjectChoice();
  const adopt = useAdoptStatic();
  const saveApp = useSaveApp();

  const rootRoute = site.domains[0]?.routes.find((r) => r.targetType === "static" && r.path === "/") ??
    site.domains[0]?.routes.find((r) => r.targetType === "static");
  const current = site.roots[0]?.root ?? rootRoute?.root ?? "";

  const [f, setF] = useState<StaticForm>({
    slug: suggestSlug(site.name),
    sourceType: "github",
    repo: "",
    branch: "main",
    staticBuildCmd: "",
    staticOutputDir: STATIC_DEFAULTS.outputDir,
    staticBaseDir: STATIC_DEFAULTS.baseDir,
    staticImage: STATIC_DEFAULTS.image,
    staticImmutablePrefix: rootRoute?.immutablePrefix || STATIC_DEFAULTS.immutablePrefix,
    staticSpa: rootRoute?.spa ?? false,
  });
  const [errors, setErrors] = useState<Errors>({});
  const [formError, setFormError] = useState<ReactNode>(null);
  const [done, setDone] = useState<{ app: AppView; domains: string[] } | null>(null);
  const [actionsOff, setActionsOff] = useState(false);

  const set = <K extends keyof StaticForm>(k: K, v: StaticForm[K]) => {
    setF((cur) => ({ ...cur, [k]: v }));
    setErrors((e) => ({ ...e, [k]: undefined }));
  };

  const busy = adopt.isPending || pc.pending;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    const next: Errors = {};
    next.projectName = pc.check();
    if (!validSlug(f.slug)) next.slug = f.slug ? SLUG_ERROR : "Informe o nome da aplicação.";
    const repo = f.repo.trim();
    if (f.sourceType === "github" && !validRepo(repo)) next.repo = "Escolha o repositório.";
    if (f.sourceType === "git" && !validGitUrl(repo)) next.repo = "URL inválida. Use https://host/caminho, sem usuário nem senha.";
    if (!validBranch(f.branch.trim())) next.branch = "Branch inválido.";
    if (!validRelPath(f.staticOutputDir.trim())) next.staticOutputDir = REL_ERROR;
    if (!validRelPath(f.staticBaseDir.trim())) next.staticBaseDir = REL_ERROR;
    if (!validImageRef(f.staticImage.trim())) next.staticImage = "Imagem inválida. Use, por exemplo, node:22-alpine.";
    if (!f.staticImmutablePrefix.trim().startsWith("/")) next.staticImmutablePrefix = "O prefixo começa com / (por exemplo /assets/).";
    setErrors(next);
    setFormError(null);
    if (focusFirst(uid, STATIC_ORDER, next)) return;

    let projectId: number;
    try {
      projectId = await pc.resolve();
    } catch (err) {
      const pe = projectError(err);
      if (pe) setErrors({ projectName: pe });
      else setFormError(err instanceof Error ? err.message : "Erro desconhecido.");
      return;
    }
    const input: AdoptStaticInput = {
      projectId,
      slug: f.slug,
      current,
      sourceType: f.sourceType,
      repo,
      branch: f.branch.trim(),
      staticBuildCmd: f.staticBuildCmd.trim(),
      staticOutputDir: f.staticOutputDir.trim(),
      staticBaseDir: f.staticBaseDir.trim(),
      staticImage: f.staticImage.trim(),
      staticSpa: f.staticSpa,
      staticImmutablePrefix: f.staticImmutablePrefix.trim(),
    };
    adopt.mutate(input, {
      onSuccess: (r) => setDone(r),
      onError: (err) => {
        const m = adoptError(err);
        if (m.field && STATIC_ORDER.includes(m.field)) {
          setErrors({ [m.field]: m.message });
          focusFirst(uid, STATIC_ORDER, { [m.field]: m.message });
        } else {
          setFormError(m.message);
        }
      },
    });
  };

  if (done) {
    const { app } = done;
    const go = () => {
      onClose();
      router.push(appHref(app.id));
    };
    const finish = () => {
      if (!actionsOff || app.sourceType !== "github") {
        go();
        return;
      }
      saveApp.mutate({ id: app.id, input: appInputOf(app, { autoDeploy: true }) }, { onSuccess: go });
    };
    return (
      <Dialog
        open
        size="lg"
        eyebrow="Gerenciar pelo KoreGO"
        title="Site adotado"
        confirmLabel="Ir para a aplicação"
        hideCancel
        loading={saveApp.isPending}
        onConfirm={finish}
        onCancel={go}
      >
        <p>
          <span className="font-mono text-fg" translate="no">
            {app.slug}
          </span>{" "}
          agora é uma aplicação do KoreGO, publicada em{" "}
          <span className="font-mono text-fg" translate="no">
            {app.publishRoot}
          </span>
          .{done.domains.length > 0 && (
            <>
              {" "}
              Domínios:{" "}
              <span className="font-mono text-fg" translate="no">
                {done.domains.join(", ")}
              </span>
              .
            </>
          )}
        </p>
        <p className="font-medium text-fg">Desligue o workflow do GitHub Actions que publicava o site:</p>
        <ol className="list-decimal space-y-1 pl-5 marker:font-mono marker:text-fg-subtle">
          <li>
            No GitHub, abra o repositório{" "}
            <span className="font-mono text-fg" translate="no">
              {app.repo}
            </span>{" "}
            e vá em Actions.
          </li>
          <li>Escolha o workflow que publicava o site nesta VPS.</li>
          <li>
            No menu “…”, clique em “Disable workflow”, ou apague o arquivo dele em{" "}
            <code className="font-mono text-fg-muted" translate="no">
              .github/workflows
            </code>
            .
          </li>
        </ol>
        {app.sourceType === "github" ? (
          <div>
            <Checkbox checked={actionsOff} onChange={setActionsOff} describedBy={`${uid}-actions-hint`}>
              Já desliguei o workflow do Actions; publicar a cada push
            </Checkbox>
            <Hint id={`${uid}-actions-hint`}>
              Desmarcada, o deploy automático fica desligado: publique pelo botão Deploy agora.
            </Hint>
          </div>
        ) : (
          <p>Com URL Git não há deploy a cada push: publique pelo botão Deploy agora.</p>
        )}
        {saveApp.isError && <Alert variant="danger">{saveApp.error.message}</Alert>}
      </Dialog>
    );
  }

  return (
    <Dialog
      open
      size="lg"
      eyebrow="Gerenciar pelo KoreGO"
      title={`Adotar ${site.name}`}
      confirmLabel="Adotar site"
      formId={formId}
      loading={busy}
      confirmDisabled={!current}
      onCancel={onClose}
    >
      <form id={formId} noValidate onSubmit={(e) => void submit(e)} className="space-y-4 text-fg">
        {!current && <Alert variant="danger">Este site não tem uma pasta estática para adotar.</Alert>}
        {formError && <Alert variant="danger">{formError}</Alert>}
        <p className="text-fg-muted">
          A pasta{" "}
          <span className="font-mono text-fg" translate="no">
            {current}
          </span>{" "}
          continua no ar como está. Os próximos deploys publicam novas versões ao lado dela.
        </p>
        <Alert variant="warning">
          As versões antigas fora do padrão{" "}
          <code className="font-mono" translate="no">
            AAAAMMDDHHMMSS-sha
          </code>{" "}
          ficam intocadas; as do padrão entram na limpeza que guarda 5.
        </Alert>

        <ProjectField uid={uid} pc={pc} error={errors.projectName} onEdit={() => setErrors((x) => ({ ...x, projectName: undefined }))} />
        <TextField
          id={`${uid}-slug`}
          label="Nome da aplicação"
          required
          value={f.slug}
          onChange={(v) => set("slug", v)}
          error={errors.slug}
          hint="minúsculas, números e hífen; começa com letra ou número"
        />

        <RadioCards
          name={`${uid}-source`}
          legend="Origem do código"
          value={f.sourceType}
          options={SOURCES}
          onChange={(v) => {
            setF((cur) => ({ ...cur, sourceType: v, repo: "" }));
            setErrors({});
          }}
        />
        {f.sourceType === "github" ? (
          <div>
            <p className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted" id={`${uid}-repo-label`}>
              Repositório
              <span aria-hidden="true" className="ml-1 text-accent-text">
                *
              </span>
            </p>
            <div className="mt-1.5">
              {github.isPending ? (
                <div className="flex h-12 items-center gap-2 text-[13px] text-fg-muted">
                  <Spinner size="sm" decorative />
                  <span role="status">Verificando o GitHub…</span>
                </div>
              ) : !github.data?.connected ? (
                <Alert variant="warning" title="O GitHub não está conectado.">
                  <p>
                    Conecte o GitHub App em{" "}
                    <Link href="/configuracoes/github/" className={linkCls}>
                      Configurações → GitHub
                    </Link>
                    , ou use uma URL Git pública.
                  </p>
                </Alert>
              ) : (
                <RepoPicker
                  id={`${uid}-repo`}
                  value={f.repo}
                  invalid={Boolean(errors.repo)}
                  describedBy={[errors.repo ? `${uid}-repo-error` : "", `${uid}-repo-label`].filter(Boolean).join(" ")}
                  onSelect={(r) => {
                    setF((cur) => ({ ...cur, repo: r.fullName, branch: r.defaultBranch || cur.branch }));
                    setErrors((x) => ({ ...x, repo: undefined, branch: undefined }));
                  }}
                />
              )}
            </div>
            <FieldError id={`${uid}-repo-error`}>{errors.repo}</FieldError>
            {f.repo && github.data?.connected && (
              <Hint>
                Selecionado: <span className="font-mono text-fg-muted" translate="no">{f.repo}</span>
              </Hint>
            )}
          </div>
        ) : (
          <TextField
            id={`${uid}-repo`}
            label="URL do repositório"
            required
            value={f.repo}
            onChange={(v) => set("repo", v)}
            error={errors.repo}
            placeholder="https://github.com/exemplo/site.git…"
            inputMode="url"
          />
        )}
        <TextField id={`${uid}-branch`} label="Branch" required value={f.branch} onChange={(v) => set("branch", v)} error={errors.branch} />

        <TextField
          id={`${uid}-staticBuildCmd`}
          label="Comando de build"
          value={f.staticBuildCmd}
          onChange={(v) => set("staticBuildCmd", v)}
          placeholder="npm ci && npm run build…"
          hint="Vazio: detectado pelo arquivo de lock. Sem package.json, a pasta de saída é copiada como está."
        />
        <div className="grid gap-4 sm:grid-cols-2">
          <TextField id={`${uid}-staticOutputDir`} label="Pasta de saída" value={f.staticOutputDir} onChange={(v) => set("staticOutputDir", v)} error={errors.staticOutputDir} />
          <TextField id={`${uid}-staticBaseDir`} label="Diretório base" value={f.staticBaseDir} onChange={(v) => set("staticBaseDir", v)} error={errors.staticBaseDir} />
          <TextField id={`${uid}-staticImage`} label="Imagem do build" value={f.staticImage} onChange={(v) => set("staticImage", v)} error={errors.staticImage} />
          <TextField
            id={`${uid}-staticImmutablePrefix`}
            label="Prefixo imutável"
            value={f.staticImmutablePrefix}
            onChange={(v) => set("staticImmutablePrefix", v)}
            error={errors.staticImmutablePrefix}
          />
        </div>
        <div>
          <Checkbox checked={f.staticSpa} onChange={(v) => set("staticSpa", v)} describedBy={`${uid}-spa-hint`}>
            Aplicação de página única (SPA)
          </Checkbox>
          <Hint id={`${uid}-spa-hint`}>Rotas sem arquivo voltam para o index.html.</Hint>
        </div>
      </form>
    </Dialog>
  );
}

// --- Compose ----------------------------------------------------------------------

const MODES: { value: UpdateMode; label: string; hint: string }[] = [
  { value: "pull", label: "Baixar e subir", hint: "docker compose pull e up -d" },
  { value: "script", label: "Script da stack", hint: "Roda um script da pasta da stack" },
];

const COMPOSE_ORDER = ["projectName", "slug", "updateScript", "code"];

function ComposeAdopt({ site, onClose }: { site: DetectedSite; onClose: () => void }) {
  const uid = useId();
  const formId = `${uid}-form`;
  const router = useRouter();
  const pc = useProjectChoice();
  const adopt = useAdoptCompose();
  const codeRef = useRef<HTMLInputElement>(null);

  const [slug, setSlug] = useState(() => suggestSlug(site.name));
  const [mode, setMode] = useState<UpdateMode>("pull");
  const [script, setScript] = useState("");
  const [code, setCode] = useState("");
  const [errors, setErrors] = useState<Errors>({});
  const [formError, setFormError] = useState<ReactNode>(null);
  const [adopted, setAdopted] = useState(false);

  const busy = adopt.isPending || pc.pending || adopted;
  const clear = (k: string) => setErrors((e) => ({ ...e, [k]: undefined }));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    const next: Errors = {};
    next.projectName = pc.check();
    if (!validSlug(slug)) next.slug = slug ? SLUG_ERROR : "Informe o nome da aplicação.";
    const clean = cleanAuthCode(code);
    if (mode === "script") {
      if (!script.trim()) next.updateScript = "Informe o script, relativo à pasta da stack (por exemplo deploy.sh).";
      if (!validAuthCode(clean)) next.code = clean ? CODE_FORMAT_ERROR : "Informe o código de confirmação.";
    }
    setErrors(next);
    setFormError(null);
    if (focusFirst(uid, COMPOSE_ORDER, next)) return;

    let projectId: number;
    try {
      projectId = await pc.resolve();
    } catch (err) {
      const pe = projectError(err);
      if (pe) setErrors({ projectName: pe });
      else setFormError(err instanceof Error ? err.message : "Erro desconhecido.");
      return;
    }
    // O código vale uma vez: não fica no campo depois de enviado.
    setCode("");
    adopt.mutate(
      {
        projectId,
        slug,
        composeProject: site.name,
        updateMode: mode,
        ...(mode === "script" ? { updateScript: script.trim(), code: clean } : {}),
      },
      {
        onSuccess: (app) => {
          setAdopted(true);
          onClose();
          router.push(appHref(app.id));
        },
        onError: (err) => {
          if (isCodeError(err)) {
            setErrors({ code: err.message });
            codeRef.current?.focus();
            return;
          }
          const m = adoptError(err);
          if (m.field && COMPOSE_ORDER.includes(m.field)) {
            setErrors({ [m.field]: m.message });
            focusFirst(uid, COMPOSE_ORDER, { [m.field]: m.message });
          } else {
            setFormError(m.message);
          }
        },
      },
    );
  };

  return (
    <Dialog
      open
      size="lg"
      eyebrow="Gerenciar pelo KoreGO"
      title={`Adotar a stack ${site.name}`}
      confirmLabel="Adotar stack"
      formId={formId}
      loading={busy}
      onCancel={onClose}
    >
      <form id={formId} noValidate onSubmit={(e) => void submit(e)} className="space-y-4 text-fg">
        {formError && <Alert variant="danger">{formError}</Alert>}
        <p className="text-fg-muted">
          A stack continua rodando onde está: nada é recriado. O KoreGO passa a iniciar, parar, reiniciar e atualizar a
          stack e a editar o .env dela.
        </p>
        <ProjectField uid={uid} pc={pc} error={errors.projectName} onEdit={() => clear("projectName")} />
        <TextField
          id={`${uid}-slug`}
          label="Nome da aplicação"
          required
          value={slug}
          onChange={(v) => {
            setSlug(v);
            clear("slug");
          }}
          error={errors.slug}
          hint="minúsculas, números e hífen; começa com letra ou número"
        />
        <RadioCards
          name={`${uid}-mode`}
          legend="Atualização"
          value={mode}
          options={MODES}
          onChange={(v) => {
            setMode(v);
            setErrors({});
          }}
        />
        {mode === "script" && (
          <>
            <TextField
              id={`${uid}-updateScript`}
              label="Script de atualização"
              required
              value={script}
              onChange={(v) => {
                setScript(v);
                clear("updateScript");
              }}
              error={errors.updateScript}
              placeholder="deploy.sh…"
              hint="Relativo à pasta da stack. Roda como root: por isso pede o código."
            />
            <CodeField
              ref={codeRef}
              id={`${uid}-code`}
              value={code}
              onChange={(v) => {
                setCode(v);
                clear("code");
              }}
              error={errors.code as string | undefined}
            />
          </>
        )}
      </form>
    </Dialog>
  );
}

/** Diálogo "Gerenciar pelo KoreGO" de um site estático ou de uma stack compose. */
export function AdoptDialog({ site, onClose }: { site: DetectedSite; onClose: () => void }) {
  if (site.kind === "estatico") return <StaticAdopt site={site} onClose={onClose} />;
  if (site.kind === "compose") return <ComposeAdopt site={site} onClose={onClose} />;
  return null;
}
