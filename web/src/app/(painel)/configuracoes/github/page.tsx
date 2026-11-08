"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useId, useState, type FormEvent, type ReactNode } from "react";
import { linkCls } from "@/components/apps/form-bits";
import { buttonLinkCls } from "@/components/apps/page-bits";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Dialog, Input, Label, Panel, Spinner } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import {
  submitManifestForm,
  useDisconnectGitHub,
  useGitHub,
  useStartGitHubManifest,
  type GitHubStatus,
} from "@/lib/github";

const ORG_RE = /^[A-Za-z0-9-]{1,39}$/;

/** Retorno do callback do GitHub (?erro=). */
const CALLBACK_ERRORS: Record<string, string> = {
  STATE_INVALID: "A conexão expirou ou não começou neste painel. Tente conectar de novo.",
  CONVERSION_FAILED:
    "O GitHub não confirmou a criação do App. Tente conectar de novo; se um App foi criado no GitHub, apague-o antes.",
  ALREADY_CONNECTED: "Já há um GitHub App conectado. Desconecte-o antes de criar outro.",
};

function connectError(err: unknown): string {
  if (!(err instanceof ApiError)) return err instanceof Error ? err.message : "Erro desconhecido.";
  switch (err.code) {
    case "ALREADY_CONNECTED":
      return CALLBACK_ERRORS.ALREADY_CONNECTED;
    case "PANEL_NOT_HTTPS":
      return "O painel precisa estar em HTTPS com domínio próprio para conectar o GitHub.";
    case "GITHUB_UNAVAILABLE":
      return "O GitHub não respondeu. Tente de novo em instantes.";
    default:
      return err.message;
  }
}

type Notice = { kind: "success" | "danger"; text: string };

/** Lê ?conectado=1 / ?erro= uma vez e limpa a URL. */
function useCallbackNotice(): [Notice | null, (n: Notice | null) => void] {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const conectado = params.get("conectado");
  const erro = params.get("erro");
  const [notice, setNotice] = useState<Notice | null>(() => {
    if (conectado === "1") {
      return {
        kind: "success",
        text: "GitHub App criado e conectado. Agora instale-o nos repositórios que o KoreGO vai publicar.",
      };
    }
    if (erro) return { kind: "danger", text: CALLBACK_ERRORS[erro] ?? "Não foi possível conectar o GitHub. Tente de novo." };
    return null;
  });
  useEffect(() => {
    if (conectado !== null || erro !== null) router.replace(pathname, { scroll: false });
  }, [conectado, erro, router, pathname]);
  return [notice, setNotice];
}

const ExternalLink = ({ href, children, className }: { href: string; children: ReactNode; className?: string }) => (
  <a href={href} target="_blank" rel="noopener noreferrer" className={className}>
    {children}
    <span aria-hidden="true"> ↗</span>
    <span className="sr-only"> (abre em nova aba)</span>
  </a>
);

function PanelNotReady() {
  return (
    <Panel title="Conectar o GitHub" variant="accent">
      <div className="max-w-prose space-y-2 text-[13px] text-fg-muted">
        <p>
          O GitHub precisa do painel em HTTPS para criar o App e enviar os eventos de push. Configure um domínio para o
          painel e acesse-o por <span className="font-mono text-fg">https://</span>.
        </p>
        <p>
          <Link href="/configuracoes/painel/" className={linkCls}>
            Ir para Configurações → Painel
          </Link>
        </p>
      </div>
    </Panel>
  );
}

function ConnectForm() {
  const uid = useId();
  const inputId = `${uid}-org`;
  const hintId = `${uid}-org-hint`;
  const errId = `${uid}-org-error`;
  const start = useStartGitHubManifest();
  const [org, setOrg] = useState("");
  const [fieldError, setFieldError] = useState("");
  const [submitError, setSubmitError] = useState("");
  const [leaving, setLeaving] = useState(false);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (start.isPending || leaving) return;
    const value = org.trim();
    if (value && !ORG_RE.test(value)) {
      setFieldError("Use só letras, números e hífens (até 39 caracteres), como no endereço da organização.");
      document.getElementById(inputId)?.focus();
      return;
    }
    setFieldError("");
    setSubmitError("");
    start.mutate(value, {
      onSuccess: (r) => {
        try {
          setLeaving(true);
          submitManifestForm(r.action, r.manifest);
        } catch (err) {
          setLeaving(false);
          setSubmitError(err instanceof Error ? err.message : "Erro desconhecido.");
        }
      },
      onError: (err) => setSubmitError(connectError(err)),
    });
  };

  const described = [fieldError ? errId : "", hintId].filter(Boolean).join(" ");

  return (
    <Panel title="Conectar o GitHub" variant="accent">
      <form noValidate onSubmit={submit} className="max-w-xl space-y-4">
        <p className="text-[13px] text-fg-muted">
          O KoreGO cria um GitHub App só seu. Você confirma no GitHub e volta para cá; depois escolhe em quais repositórios
          instalar.
        </p>
        <div>
          <Label htmlFor={inputId}>Organização (opcional)</Label>
          <Input
            id={inputId}
            name="org"
            mono
            className="mt-1.5 sm:max-w-xs"
            value={org}
            placeholder="minha-organizacao…"
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="none"
            invalid={Boolean(fieldError)}
            aria-describedby={described}
            onChange={(e) => {
              setOrg(e.target.value);
              setFieldError("");
            }}
          />
          {fieldError && (
            <p id={errId} className="mt-1 text-xs text-danger">
              {fieldError}
            </p>
          )}
          <p id={hintId} className="mt-1 text-xs text-fg-subtle">
            Deixe vazio para criar o App na sua conta pessoal.
          </p>
        </div>
        {submitError && <Alert variant="danger">{submitError}</Alert>}
        <Button type="submit" variant="primary" loading={start.isPending || leaving}>
          Conectar GitHub
        </Button>
      </form>
    </Panel>
  );
}

function Connected({ gh, onRemoved }: { gh: GitHubStatus; onRemoved: (manageUrl: string, message: string) => void }) {
  const disconnect = useDisconnectGitHub();
  const [open, setOpen] = useState(false);
  const app = gh.app!;

  const confirm = () =>
    disconnect.mutate(undefined, {
      onSuccess: (r) => {
        setOpen(false);
        onRemoved(r.manageUrl, r.message ?? "As credenciais foram apagadas do KoreGO. Remova também o App no GitHub.");
      },
    });

  const err = disconnect.error;
  const errText =
    err instanceof ApiError && err.code === "NOT_CONNECTED" ? "O GitHub já estava desconectado." : err?.message;

  return (
    <div className="space-y-4">
      <Panel
        title="GitHub App"
        variant="accent"
        actions={
          <Badge size="sm" variant="success" dot>
            Conectado
          </Badge>
        }
      >
        <dl className="-m-4 grid grid-cols-1 gap-px bg-line sm:grid-cols-2">
          <div className="bg-surface px-4 py-3">
            <dt className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">App</dt>
            <dd className="mt-1 font-mono text-[13px] text-fg" translate="no">
              <ExternalLink href={app.htmlUrl} className={linkCls}>
                {app.slug}
              </ExternalLink>
            </dd>
          </div>
          <div className="bg-surface px-4 py-3">
            <dt className="font-mono text-[10px] uppercase tracking-[0.12em] text-fg-subtle">Dono</dt>
            <dd className="mt-1 font-mono text-[13px] text-fg" translate="no">
              {app.owner}
            </dd>
          </div>
        </dl>
      </Panel>

      <Panel
        title="Instalações"
        size="sm"
        description="Repositórios em que o App está instalado podem ser publicados e recebem deploy automático a cada push."
        actions={
          <ExternalLink href={app.installUrl} className={buttonLinkCls(true)}>
            Instalar nos repositórios
          </ExternalLink>
        }
      >
        {gh.installationsError ? (
          <Alert variant="warning">{gh.installationsError}</Alert>
        ) : gh.installations.length === 0 ? (
          <p className="text-[13px] text-fg-subtle">
            Nenhuma instalação ainda. Instale o App na sua conta ou organização para escolher os repositórios.
          </p>
        ) : (
          <ul aria-label="Instalações" className="divide-y divide-line border border-line">
            {gh.installations.map((i) => (
              <li key={i.id} className="flex items-center justify-between gap-3 px-3 py-2">
                <span className="font-mono text-[13px] text-fg" translate="no">
                  {i.account}
                </span>
                <span className="font-mono text-[11px] tabular-nums text-fg-subtle">#{i.id}</span>
              </li>
            ))}
          </ul>
        )}
      </Panel>

      <Panel title="Desconectar" variant="danger" size="sm">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="max-w-prose text-[13px] text-fg-muted">
            Apaga as credenciais do App neste painel. Aplicações do GitHub param de receber deploys.
          </p>
          <Button
            variant="danger"
            onClick={() => {
              disconnect.reset();
              setOpen(true);
            }}
          >
            Desconectar
          </Button>
        </div>
      </Panel>
      <Dialog
        open={open}
        variant="danger"
        title="Desconectar o GitHub?"
        confirmLabel="Desconectar"
        loading={disconnect.isPending}
        onCancel={() => setOpen(false)}
        onConfirm={confirm}
      >
        <p>
          O KoreGO apaga a chave e os segredos do App{" "}
          <span className="font-mono text-fg" translate="no">
            {app.slug}
          </span>
          . Deploys de aplicações do GitHub e o deploy automático param até você conectar de novo.
        </p>
        <p>O App continua existindo no GitHub: depois de desconectar, apague o App no GitHub para revogar o acesso de vez.</p>
        {disconnect.isError && <Alert variant="danger">{errText}</Alert>}
      </Dialog>
    </div>
  );
}

function GitHubSettings() {
  const gh = useGitHub();
  const [notice, setNotice] = useCallbackNotice();
  const [removed, setRemoved] = useState<{ manageUrl: string; message: string } | null>(null);

  let body: ReactNode;
  if (gh.isPending) {
    body = (
      <Panel>
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando…</span>
        </PanelState>
      </Panel>
    );
  } else if (gh.isError) {
    body = (
      <Alert variant="danger" title="Não foi possível carregar a conexão do GitHub.">
        <p>{gh.error.message}</p>
        <Button size="sm" className="mt-2" loading={gh.isFetching} onClick={() => void gh.refetch()}>
          Tentar novamente
        </Button>
      </Alert>
    );
  } else if (gh.data.connected && gh.data.app) {
    body = (
      <Connected
        gh={gh.data}
        onRemoved={(manageUrl, message) => {
          setNotice(null);
          setRemoved({ manageUrl, message });
        }}
      />
    );
  } else if (!gh.data.panelReady) {
    body = <PanelNotReady />;
  } else {
    body = <ConnectForm />;
  }

  return (
    <>
      <PageHeader
        eyebrow="Configurações"
        title="GitHub"
        description="Publique repositórios privados e faça deploy automático a cada push."
      />
      <div className="max-w-3xl space-y-4">
        {notice && <Alert variant={notice.kind}>{notice.text}</Alert>}
        {removed && (
          <Alert variant="warning" title="GitHub desconectado.">
            <p>{removed.message}</p>
            {removed.manageUrl && (
              <p className="mt-1">
                <ExternalLink href={removed.manageUrl} className={cn(linkCls, "font-medium")}>
                  Apagar o App no GitHub
                </ExternalLink>
              </p>
            )}
          </Alert>
        )}
        {body}
      </div>
    </>
  );
}

export default function GitHubPage() {
  return (
    <Suspense
      fallback={
        <Panel>
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">Carregando…</span>
          </PanelState>
        </Panel>
      }
    >
      <GitHubSettings />
    </Suspense>
  );
}
