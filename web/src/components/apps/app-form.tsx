"use client";

import Link from "next/link";
import { useId, useState, type FormEvent, type ReactNode } from "react";
import { Alert, Button, Input, Label, Spinner } from "@/components/ui";
import { ApiError } from "@/lib/api";
import {
  defaultsFor,
  useSaveApp,
  type AppField,
  type AppInput,
  type AppView,
  type BuildType,
  type SourceType,
} from "@/lib/apps";
import { cn } from "@/lib/cn";
import { useGitHub } from "@/lib/github";
import {
  validBranch,
  validGitUrl,
  validImageRef,
  validPort,
  validRelPath,
  validRepo,
  validServiceName,
  validSlug,
} from "@/lib/validate";
import { Checkbox, FieldError, Hint, linkCls, selectCls } from "./form-bits";
import { NoGitHubMessage } from "./page-bits";
import { RepoPicker } from "./repo-picker";

export const COMPOSE_WARNING =
  "Compose recria os containers a cada deploy: alguns segundos fora do ar. Rollback não disponível.";

const SLUG_HINT = "minúsculas, números e hífen; começa com letra ou número";
const SLUG_ERROR = "Nome inválido: use só minúsculas, números e hífen, começando com letra ou número.";
const DEFAULT_HEALTH = 60;

const SOURCES: { value: SourceType; label: string; hint: string }[] = [
  { value: "github", label: "GitHub", hint: "Repositório do GitHub App" },
  { value: "git", label: "URL Git pública", hint: "Clone https sem credenciais" },
  { value: "image", label: "Imagem", hint: "Imagem pronta de um registro" },
];

const BUILDS: { value: Exclude<BuildType, "image">; label: string }[] = [
  { value: "dockerfile", label: "Dockerfile" },
  { value: "compose", label: "Docker Compose" },
  { value: "railpack", label: "Railpack (detecta a linguagem)" },
];

/** Ordem dos campos na tela: o primeiro com erro recebe o foco. */
const ORDER: AppField[] = [
  "repo",
  "image",
  "branch",
  "dockerfilePath",
  "composePath",
  "composeService",
  "slug",
  "internalPort",
  "healthTimeoutS",
];

type Errors = Partial<Record<AppField, ReactNode>>;

interface FormState {
  sourceType: SourceType;
  buildType: BuildType;
  slug: string;
  repo: string;
  image: string;
  branch: string;
  dockerfilePath: string;
  composePath: string;
  composeService: string;
  internalPort: string;
  healthTimeoutS: string;
  autoDeploy: boolean;
}

function initialState(app?: AppView): FormState {
  if (app) {
    const d = defaultsFor(app.sourceType, app.buildType);
    return {
      sourceType: app.sourceType,
      buildType: app.buildType,
      slug: app.slug,
      repo: app.repo,
      image: app.image,
      branch: app.branch || d.branch,
      dockerfilePath: app.dockerfilePath || d.dockerfilePath,
      composePath: app.composePath || d.composePath,
      composeService: app.composeService,
      internalPort: String(app.internalPort || ""),
      healthTimeoutS: String(app.healthTimeoutS || DEFAULT_HEALTH),
      autoDeploy: app.autoDeploy,
    };
  }
  const d = defaultsFor("github", "dockerfile");
  return {
    sourceType: "github",
    buildType: "dockerfile",
    slug: "",
    repo: "",
    image: "",
    branch: d.branch,
    dockerfilePath: d.dockerfilePath,
    composePath: d.composePath,
    composeService: "",
    internalPort: "",
    healthTimeoutS: String(DEFAULT_HEALTH),
    autoDeploy: true,
  };
}

/** Valida os campos visíveis; devolve as mensagens por campo. */
function validate(s: FormState, visible: AppField[], editing: boolean): Errors {
  const e: Errors = {};
  const has = (f: AppField) => visible.includes(f);
  if (!editing && !validSlug(s.slug)) e.slug = s.slug ? SLUG_ERROR : "Informe o nome da aplicação.";
  if (has("repo")) {
    const repo = s.repo.trim();
    if (s.sourceType === "github" && !validRepo(repo)) e.repo = "Escolha o repositório.";
    if (s.sourceType === "git" && !validGitUrl(repo)) {
      e.repo = "URL inválida. Use https://host/caminho, sem usuário nem senha.";
    }
  }
  if (has("image") && !validImageRef(s.image.trim())) e.image = "Imagem inválida. Use, por exemplo, nginx:1-alpine.";
  if (has("branch") && !validBranch(s.branch.trim())) e.branch = "Branch inválido.";
  const rel = "Caminho inválido. Use um caminho relativo dentro do repositório.";
  if (has("dockerfilePath") && s.dockerfilePath.trim() && !validRelPath(s.dockerfilePath.trim())) e.dockerfilePath = rel;
  if (has("composePath") && s.composePath.trim() && !validRelPath(s.composePath.trim())) e.composePath = rel;
  if (has("composeService") && !validServiceName(s.composeService.trim())) {
    e.composeService = "Informe o serviço do Compose que recebe o tráfego.";
  }
  if (!validPort(Number(s.internalPort)) || s.internalPort.trim() === "") {
    e.internalPort = "Informe a porta interna da aplicação (1 a 65535).";
  }
  const h = s.healthTimeoutS.trim() === "" ? 0 : Number(s.healthTimeoutS);
  if (!Number.isInteger(h) || (h !== 0 && (h < 10 || h > 600))) {
    e.healthTimeoutS = "Use de 10 a 600 segundos, ou 0 para o padrão (60).";
  }
  return e;
}

function toInput(s: FormState, projectId: number): AppInput {
  const image = s.sourceType === "image";
  const compose = !image && s.buildType === "compose";
  const health = s.healthTimeoutS.trim() === "" ? 0 : Number(s.healthTimeoutS);
  return {
    projectId,
    slug: s.slug,
    sourceType: s.sourceType,
    buildType: image ? "image" : s.buildType,
    repo: image ? "" : s.repo.trim(),
    image: image ? s.image.trim() : "",
    branch: image ? "" : s.branch.trim(),
    // Só o build dockerfile usa o caminho; vazio, o servidor aplica o padrão "Dockerfile".
    dockerfilePath: !image && s.buildType === "dockerfile" ? s.dockerfilePath.trim() : "",
    composePath: compose ? s.composePath.trim() : "",
    composeService: compose ? s.composeService.trim() : "",
    internalPort: Number(s.internalPort),
    healthTimeoutS: health,
    autoDeploy: s.sourceType === "github" && s.autoDeploy,
  };
}


/** Erro da API → campo (quando houver) e mensagem. */
function apiErrorOf(err: unknown, visible: AppField[]): { field?: AppField; message: ReactNode } {
  if (!(err instanceof ApiError)) return { message: err instanceof Error ? err.message : "Erro desconhecido." };
  switch (err.code) {
    case "INVALID_APP": {
      const f = err.field as AppField | undefined;
      return f && (visible.includes(f) || f === "internalPort") ? { field: f, message: err.message } : { message: err.message };
    }
    case "APP_EXISTS":
      return { field: "slug", message: "Já existe uma aplicação com esse nome." };
    case "NO_GITHUB":
      return { message: <NoGitHubMessage /> };
    case "APP_DELETING":
      return { message: "A aplicação está sendo removida." };
    default:
      return { message: err.message };
  }
}

export interface AppFormProps {
  projectId: number;
  /** Aplicação em edição; sem ela, cria uma nova. */
  app?: AppView;
  onSaved: (app: AppView) => void;
}

/** Formulário de aplicação (criar e editar). Os campos visíveis seguem `defaultsFor`. */
export function AppForm({ projectId, app, onSaved }: AppFormProps) {
  const uid = useId();
  const id = (f: string) => `${uid}-${f}`;
  const errId = (f: AppField) => `${uid}-${f}-error`;
  const editing = Boolean(app);

  const save = useSaveApp();
  const github = useGitHub();
  const [s, setS] = useState<FormState>(() => initialState(app));
  const [errors, setErrors] = useState<Errors>({});
  const [formError, setFormError] = useState<ReactNode>(null);
  const [saved, setSaved] = useState(false);

  const d = defaultsFor(s.sourceType, s.buildType);
  const visible = d.visible;
  const has = (f: AppField) => visible.includes(f);
  const req = (f: AppField) => d.required.includes(f);

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => {
    setS((cur) => ({ ...cur, [k]: v }));
    setSaved(false);
    if (k in errors) setErrors((e) => ({ ...e, [k]: undefined }));
  };

  const changeSource = (src: SourceType) => {
    setS((cur) => {
      if (cur.sourceType === src) return cur;
      const buildType: BuildType = src === "image" ? "image" : cur.buildType === "image" ? "dockerfile" : cur.buildType;
      // github guarda dono/nome e git guarda uma URL: o valor de um não serve no outro.
      return { ...cur, sourceType: src, buildType, repo: "" };
    });
    setErrors({});
    setSaved(false);
  };

  const liveSlug = !editing && s.slug !== "" && !validSlug(s.slug) ? SLUG_ERROR : "";
  const slugError = errors.slug ?? (liveSlug || undefined);
  const errorOf = (f: AppField) => (f === "slug" ? slugError : errors[f]);
  const describe = (f: AppField, ...extra: string[]) =>
    [errorOf(f) ? errId(f) : "", ...extra].filter(Boolean).join(" ") || undefined;

  const focusField = (f: AppField) => {
    const target = f === "repo" && s.sourceType === "github" ? `${id("repo")}-search` : id(f);
    requestAnimationFrame(() => document.getElementById(target)?.focus());
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (save.isPending) return;
    const next = validate(s, visible, editing);
    setErrors(next);
    setFormError(null);
    setSaved(false);
    const first = ORDER.find((f) => next[f]);
    if (first) {
      focusField(first);
      return;
    }
    save.mutate(
      { id: app?.id, input: toInput(s, projectId) },
      {
        onSuccess: (a) => {
          setSaved(true);
          onSaved(a);
        },
        onError: (err) => {
          const m = apiErrorOf(err, visible);
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

  const text = (
    f: AppField,
    label: string,
    opts: { placeholder?: string; hint?: ReactNode; mono?: boolean; inputMode?: "url" | "text" } = {},
  ) => (
    <div>
      <Label htmlFor={id(f)} required={req(f)}>
        {label}
      </Label>
      <Input
        id={id(f)}
        name={f}
        mono={opts.mono ?? true}
        className="mt-1.5"
        value={s[f as keyof FormState] as string}
        placeholder={opts.placeholder}
        autoComplete="off"
        spellCheck={false}
        autoCapitalize="none"
        inputMode={opts.inputMode}
        invalid={Boolean(errors[f])}
        aria-describedby={describe(f, opts.hint ? id(`${f}-hint`) : "")}
        onChange={(e) => set(f as keyof FormState, e.target.value as never)}
      />
      <FieldError id={errId(f)}>{errors[f]}</FieldError>
      {opts.hint && <Hint id={id(`${f}-hint`)}>{opts.hint}</Hint>}
    </div>
  );

  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      {formError && <Alert variant="danger">{formError}</Alert>}

      <Section title="Origem" step="01">
        <fieldset>
          <legend className="sr-only">Origem do código</legend>
          <div className="grid gap-px border border-line-strong bg-line-strong sm:grid-cols-3">
            {SOURCES.map((o) => {
              const checked = s.sourceType === o.value;
              return (
                <label
                  key={o.value}
                  className={cn(
                    "relative flex cursor-pointer flex-col gap-0.5 px-3 py-2.5 motion-safe:transition-colors motion-safe:duration-100",
                    "has-[:focus-visible]:outline-solid has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-[-2px] has-[:focus-visible]:outline-accent-text",
                    checked
                      ? "bg-surface-2 shadow-[inset_2px_0_0_0_var(--color-accent)]"
                      : "bg-surface hover:bg-surface-2",
                  )}
                >
                  <input
                    type="radio"
                    name={id("source")}
                    value={o.value}
                    checked={checked}
                    onChange={() => changeSource(o.value)}
                    aria-describedby={id(`source-${o.value}-hint`)}
                    className="sr-only"
                  />
                  <span className={cn("text-[13px] font-medium", checked ? "text-fg" : "text-fg-muted")}>{o.label}</span>
                  <span id={id(`source-${o.value}-hint`)} className="text-[11px] text-fg-subtle">
                    {o.hint}
                  </span>
                </label>
              );
            })}
          </div>
        </fieldset>

        {s.sourceType === "github" && (
          <div>
            <p className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted" id={id("repo-label")}>
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
                    Para publicar de um repositório do GitHub, conecte o GitHub App em{" "}
                    <Link href="/configuracoes/github/" className={linkCls}>
                      Configurações → GitHub
                    </Link>
                    . Ou use uma URL Git pública.
                  </p>
                </Alert>
              ) : (
                <RepoPicker
                  id={id("repo")}
                  value={s.repo}
                  invalid={Boolean(errors.repo)}
                  describedBy={describe("repo", id("repo-label"))}
                  onSelect={(r) => {
                    setS((cur) => ({ ...cur, repo: r.fullName, branch: cur.branch.trim() ? cur.branch : r.defaultBranch }));
                    setErrors((e) => ({ ...e, repo: undefined, branch: undefined }));
                    setSaved(false);
                  }}
                />
              )}
            </div>
            <FieldError id={errId("repo")}>{errors.repo}</FieldError>
            {s.repo && github.data?.connected && (
              <Hint>
                Selecionado: <span className="font-mono text-fg-muted" translate="no">{s.repo}</span>
              </Hint>
            )}
          </div>
        )}
        {s.sourceType === "git" &&
          text("repo", "URL do repositório", {
            placeholder: "https://github.com/exemplo/site.git…",
            inputMode: "url",
            hint: "Repositório público, acessível sem senha.",
          })}
        {has("image") && text("image", "Imagem", { placeholder: "nginx:1-alpine…", hint: "Do Docker Hub ou de outro registro público." })}
        {has("branch") && text("branch", "Branch", { placeholder: "main…" })}
      </Section>

      {s.sourceType !== "image" && (
        <Section title="Build" step="02">
          <div>
            <Label htmlFor={id("buildType")} required>
              Tipo de build
            </Label>
            <select
              id={id("buildType")}
              name="buildType"
              className={cn(selectCls, "mt-1.5")}
              value={s.buildType}
              onChange={(e) => set("buildType", e.target.value as BuildType)}
            >
              {BUILDS.map((b) => (
                <option key={b.value} value={b.value}>
                  {b.label}
                </option>
              ))}
            </select>
          </div>
          {s.buildType === "compose" && <Alert variant="warning">{COMPOSE_WARNING}</Alert>}
          <div className="grid gap-4 sm:grid-cols-2">
            {has("dockerfilePath") && text("dockerfilePath", "Caminho do Dockerfile", { placeholder: `${d.dockerfilePath}…` })}
            {has("composePath") && text("composePath", "Caminho do Compose", { placeholder: `${d.composePath}…` })}
            {has("composeService") &&
              text("composeService", "Serviço de entrada", {
                placeholder: "web…",
                hint: "O serviço do Compose que recebe o tráfego.",
              })}
          </div>
        </Section>
      )}

      <Section title="Execução" step={s.sourceType === "image" ? "02" : "03"}>
        <div>
          <Label htmlFor={id("slug")} required={!editing}>
            Nome
          </Label>
          <Input
            id={id("slug")}
            name="slug"
            mono
            className={cn("mt-1.5", editing && "border-dashed bg-bg text-fg-muted hover:border-line-strong")}
            value={s.slug}
            readOnly={editing}
            placeholder="loja-web…"
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="none"
            invalid={Boolean(slugError)}
            aria-describedby={describe("slug", id("slug-hint"))}
            onChange={(e) => set("slug", e.target.value)}
          />
          <FieldError id={errId("slug")}>{slugError}</FieldError>
          <Hint id={id("slug-hint")}>
            {editing ? "O nome não pode ser alterado depois de criado." : `Identifica a aplicação nos containers: ${SLUG_HINT}.`}
          </Hint>
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <Label htmlFor={id("internalPort")} required>
              Porta interna
            </Label>
            <Input
              id={id("internalPort")}
              name="internalPort"
              type="number"
              inputMode="numeric"
              min={1}
              max={65535}
              mono
              className="mt-1.5 tabular-nums"
              value={s.internalPort}
              placeholder="3000…"
              autoComplete="off"
              invalid={Boolean(errors.internalPort)}
              aria-describedby={describe("internalPort", id("internalPort-hint"))}
              onChange={(e) => set("internalPort", e.target.value)}
            />
            <FieldError id={errId("internalPort")}>{errors.internalPort}</FieldError>
            <Hint id={id("internalPort-hint")}>Porta em que a aplicação escuta dentro do container.</Hint>
          </div>
          <div>
            <Label htmlFor={id("healthTimeoutS")}>Verificação de saúde (s)</Label>
            <Input
              id={id("healthTimeoutS")}
              name="healthTimeoutS"
              type="number"
              inputMode="numeric"
              min={0}
              max={600}
              mono
              className="mt-1.5 tabular-nums"
              value={s.healthTimeoutS}
              autoComplete="off"
              invalid={Boolean(errors.healthTimeoutS)}
              aria-describedby={describe("healthTimeoutS", id("healthTimeoutS-hint"))}
              onChange={(e) => set("healthTimeoutS", e.target.value)}
            />
            <FieldError id={errId("healthTimeoutS")}>{errors.healthTimeoutS}</FieldError>
            <Hint id={id("healthTimeoutS-hint")}>Quanto esperar a aplicação responder: 10 a 600 s.</Hint>
          </div>
        </div>

        {s.sourceType === "github" && (
          <div>
            <Checkbox checked={s.autoDeploy} onChange={(v) => set("autoDeploy", v)} describedBy={id("autoDeploy-hint")}>
              Deploy automático a cada push
            </Checkbox>
            <Hint id={id("autoDeploy-hint")}>Cada push no branch publica uma nova versão.</Hint>
          </div>
        )}
      </Section>

      <div className="flex flex-wrap items-center justify-end gap-3 border-t border-line pt-4">
        {saved && editing && (
          <span role="status" className="text-xs text-success">
            Alterações salvas.
          </span>
        )}
        <Button type="submit" variant="primary" loading={save.isPending}>
          {editing ? "Salvar alterações" : "Criar aplicação"}
        </Button>
      </div>
    </form>
  );
}

function Section({ title, step, children }: { title: string; step: string; children: ReactNode }) {
  const hid = useId();
  return (
    <section aria-labelledby={hid} className="border border-line bg-surface">
      <header className="flex items-baseline gap-3 border-b border-line px-4 py-2.5">
        <span aria-hidden="true" className="font-mono text-[10px] tabular-nums text-accent-text">
          {step}
        </span>
        <h2 id={hid} className="text-sm font-semibold tracking-tight text-fg">
          {title}
        </h2>
      </header>
      <div className="space-y-4 p-4">{children}</div>
    </section>
  );
}

