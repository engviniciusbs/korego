"use client";

import Link from "next/link";
import { useId, useState, type FormEvent } from "react";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Dialog, Input, Label, Panel, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { Tabs, tabId, tabPanelId } from "@/components/ui/tabs";
import { mcpClients, mcpOAuthClients, type McpClientId, type McpClientSetup } from "@/lib/mcp-clients";
import { usePanelSettings } from "@/lib/settings";
import { selectCls } from "@/components/domains/form-bits";
import { ApiError } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import {
  useCreateToken,
  useRevokeToken,
  useTokens,
  type ApiToken,
  type CreateTokenResult,
} from "@/lib/tokens";

const th = "px-4 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const td = "px-4 py-2.5 align-middle";

const MAX_NAME = 64;

function CopyButton({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button
      size="sm"
      aria-label={label}
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(
          () => {
            setDone(true);
            setTimeout(() => setDone(false), 2000);
          },
          () => {},
        );
      }}
    >
      {done ? "Copiado" : "Copiar"}
    </Button>
  );
}

const codeCls =
  "block min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap border border-line-strong bg-bg/40 px-3 py-2 font-mono text-xs text-fg [overflow-wrap:anywhere]";

function ConnectClients({ clients }: { clients: McpClientSetup[] }) {
  const uid = useId();
  const [active, setActive] = useState<McpClientId>("claude-code");
  const c = clients.find((x) => x.id === active) ?? clients[0];
  return (
    <div>
      <Label>Conectar um agente</Label>
      <Tabs
        id={uid}
        label="Cliente MCP"
        className="mt-1.5 flex flex-wrap"
        items={clients.map((x) => ({ value: x.id, label: x.label }))}
        value={c.id}
        onChange={setActive}
      />
      <div role="tabpanel" id={tabPanelId(uid)} aria-labelledby={tabId(uid, c.id)} className="mt-3 space-y-2">
        <p className="text-xs text-fg-muted">{c.hint}</p>
        {c.installLink && (
          <a
            href={c.installLink.href}
            className={`inline-flex h-7 touch-manipulation items-center border border-accent bg-accent px-2.5 text-xs font-medium tracking-tight text-accent-fg hover:border-accent-hover hover:bg-accent-hover ${focusRing}`}
          >
            {c.installLink.label}
          </a>
        )}
        <div className="flex items-start gap-2">
          <code className={codeCls} translate="no">
            {c.snippet}
          </code>
          <CopyButton text={c.snippet} label={`Copiar configuração do ${c.label}`} />
        </div>
      </div>
    </div>
  );
}

/** Conexão por OAuth: o agente recebe só a URL e o navegador pede a autorização. */
function ConnectAgent() {
  const panel = usePanelSettings();
  const ready = panel.data && panel.data.domain && panel.data.cert?.state === "active";
  return (
    <Panel
      title="Conectar um agente"
      description="Informe só a URL ao agente: o navegador abre para você autorizar com o 2FA."
    >
      {panel.isPending ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando…</span>
        </PanelState>
      ) : ready ? (
        <div className="space-y-3">
          <ConnectClients clients={mcpOAuthClients(`https://${panel.data.domain}`)} />
          <p className="text-xs text-fg-subtle">Use exatamente esta URL; outro endereço do mesmo servidor não autoriza.</p>
        </div>
      ) : (
        <Alert variant="warning">
          <p>Configure o domínio do painel com HTTPS para conectar agentes.</p>
          <p className="mt-1">
            <Link href="/configuracoes/painel/" className="underline">
              Configurações → Painel
            </Link>
          </p>
        </Alert>
      )}
    </Panel>
  );
}

function CreateDialog({ onClose }: { onClose: () => void }) {
  const uid = useId();
  const formId = `${uid}-form`;
  const create = useCreateToken();
  const [name, setName] = useState("");
  const [days, setDays] = useState("90");
  const [code, setCode] = useState("");
  const [nameError, setNameError] = useState("");
  // O valor em claro vive só aqui: fechar o diálogo desmonta o componente e o descarta.
  const [created, setCreated] = useState<CreateTokenResult | null>(null);

  if (created) {
    return (
      <Dialog
        open
        size="lg"
        eyebrow="Tokens de API"
        title="Token criado"
        confirmLabel="Fechar"
        hideCancel
        onConfirm={onClose}
        onCancel={onClose}
      >
        <Alert variant="warning">Copie o token agora. Ele não será exibido novamente.</Alert>
        <div>
          <Label>Token</Label>
          <div className="mt-1.5 flex items-start gap-2">
            <code className="block min-w-0 flex-1 border border-line-strong bg-bg/40 px-3 py-2 font-mono text-xs text-fg [overflow-wrap:anywhere]" translate="no">
              {created.token}
            </code>
            <CopyButton text={created.token} label="Copiar token" />
          </div>
        </div>
        <ConnectClients clients={mcpClients(location.origin, created.token)} />
      </Dialog>
    );
  }

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const n = name.trim();
    if (!n || [...n].length > MAX_NAME) {
      setNameError(`Informe um nome de 1 a ${MAX_NAME} caracteres.`);
      document.getElementById(`${uid}-name`)?.focus();
      return;
    }
    setNameError("");
    create.mutate(
      { name: n, expiresInDays: Number(days) as 0 | 30 | 90, code: code.trim() },
      {
        onSuccess: (r) => {
          setCode("");
          setCreated(r);
        },
        onError: (err) => {
          setCode("");
          if (err instanceof ApiError && err.code === "TOKEN_NAME_TAKEN") {
            setNameError("Já existe um token com esse nome.");
            document.getElementById(`${uid}-name`)?.focus();
          }
        },
      },
    );
  };

  const err = create.error;
  const nameTaken = err instanceof ApiError && err.code === "TOKEN_NAME_TAKEN";

  return (
    <Dialog
      open
      size="lg"
      eyebrow="Tokens de API"
      title="Novo token"
      confirmLabel="Criar token"
      formId={formId}
      loading={create.isPending}
      onCancel={onClose}
    >
      <form id={formId} noValidate onSubmit={submit} className="space-y-4 text-fg">
        {err && !nameTaken && (
          <Alert variant="danger">
            <p>{err.message}</p>
            {err instanceof ApiError && err.code === "TOKEN_REQUIRES_HTTPS" && (
              <p className="mt-1">
                <Link href="/configuracoes/painel/" className="underline">
                  Configurações → Painel
                </Link>
              </p>
            )}
          </Alert>
        )}
        <div>
          <Label htmlFor={`${uid}-name`} required>
            Nome
          </Label>
          <Input
            id={`${uid}-name`}
            className="mt-1.5"
            value={name}
            maxLength={MAX_NAME}
            placeholder="claude-code"
            autoComplete="off"
            invalid={Boolean(nameError)}
            aria-describedby={nameError ? `${uid}-name-err` : undefined}
            onChange={(e) => {
              setName(e.target.value);
              setNameError("");
            }}
          />
          {nameError && (
            <p id={`${uid}-name-err`} className="mt-1 text-xs text-danger">
              {nameError}
            </p>
          )}
        </div>
        <div>
          <Label htmlFor={`${uid}-days`}>Validade</Label>
          <select id={`${uid}-days`} className={`${selectCls} mt-1.5`} value={days} onChange={(e) => setDays(e.target.value)}>
            <option value="90">90 dias</option>
            <option value="30">30 dias</option>
            <option value="0">Nunca</option>
          </select>
        </div>
        <div>
          <Label htmlFor={`${uid}-code`} required>
            Código 2FA
          </Label>
          <Input
            id={`${uid}-code`}
            mono
            className="mt-1.5"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={8}
            placeholder="000000"
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
          <p className="mt-1 text-xs text-fg-subtle">Código atual do aplicativo autenticador.</p>
        </div>
      </form>
    </Dialog>
  );
}

function TokenList() {
  const tokens = useTokens();
  const revoke = useRevokeToken();
  const [confirming, setConfirming] = useState<number | null>(null);
  const [creating, setCreating] = useState(false);
  const [now] = useState(() => Date.now());
  const items = tokens.data?.items ?? [];

  return (
    <Panel
      title="Tokens de API"
      description="Permitem que agentes (como o Claude Code) usem o painel pelo endpoint MCP. Revogar bloqueia o token na próxima chamada."
      actions={
        <Button size="sm" variant="primary" onClick={() => setCreating(true)}>
          Novo token
        </Button>
      }
    >
      {revoke.isError && (
        <Alert variant="danger" className="mb-3">
          Não foi possível revogar o token: {revoke.error.message}
        </Alert>
      )}
      {tokens.isPending ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando tokens…</span>
        </PanelState>
      ) : tokens.isError ? (
        <Alert variant="danger" title="Não foi possível carregar os tokens.">
          <p>{tokens.error.message}</p>
          <Button size="sm" className="mt-2" onClick={() => void tokens.refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      ) : items.length === 0 ? (
        <PanelState>Nenhum token criado.</PanelState>
      ) : (
        <div className="-m-4 overflow-x-auto">
          <table className="w-full min-w-[720px] border-collapse text-[13px]">
            <thead className="border-b border-line">
              <tr>
                <th scope="col" className={th}>Nome</th>
                <th scope="col" className={th}>Prefixo</th>
                <th scope="col" className={th}>Último uso</th>
                <th scope="col" className={th}>Expira em</th>
                <th scope="col" className={`${th} text-right`}>
                  <span className="sr-only">Ações</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((t: ApiToken) => (
                <tr key={t.id} className="border-b border-line last:border-b-0 hover:bg-surface-2/60">
                  <td className={`${td} text-fg [overflow-wrap:anywhere]`}>
                    {t.name}
                    {t.kind === "oauth" && (
                      <Badge size="sm" className="ml-2">
                        OAuth
                      </Badge>
                    )}
                  </td>
                  <td className={`${td} font-mono text-fg-muted`} translate="no">
                    {t.prefix}…
                  </td>
                  <td className={`${td} tabular-nums text-fg-muted`}>
                    {t.lastUsedAt ? (
                      <>
                        {formatDateTime(t.lastUsedAt)}
                        {t.lastUsedIp && (
                          <span className="ml-2 font-mono text-xs text-fg-subtle" translate="no">
                            {t.lastUsedIp}
                          </span>
                        )}
                      </>
                    ) : (
                      "nunca"
                    )}
                  </td>
                  <td className={`${td} tabular-nums text-fg-muted`}>
                    {t.expiresAt ? (
                      new Date(t.expiresAt).getTime() < now ? (
                        <>
                          {formatDateTime(t.expiresAt)}{" "}
                          <span className="text-xs text-danger">expirado</span>
                        </>
                      ) : (
                        formatDateTime(t.expiresAt)
                      )
                    ) : (
                      "nunca"
                    )}
                  </td>
                  <td className={`${td} text-right`}>
                    {confirming === t.id ? (
                      <div className="inline-flex items-center gap-2">
                        <span className="text-xs text-fg-muted">Revogar este token?</span>
                        <Button
                          size="sm"
                          variant="danger"
                          autoFocus
                          loading={revoke.isPending}
                          onClick={() => revoke.mutate(t.id, { onSettled: () => setConfirming(null) })}
                        >
                          Confirmar
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => setConfirming(null)}>
                          Cancelar
                        </Button>
                      </div>
                    ) : (
                      <Button
                        size="sm"
                        aria-label={`Revogar token ${t.name}`}
                        onClick={() => {
                          revoke.reset();
                          setConfirming(t.id);
                        }}
                      >
                        Revogar
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {creating && <CreateDialog onClose={() => setCreating(false)} />}
    </Panel>
  );
}

export default function TokensPage() {
  return (
    <>
      <PageHeader
        eyebrow="Configurações"
        title="Tokens de API"
        description="Acesso de agentes ao painel via MCP. Exigem o painel em HTTPS."
      />
      <div className="space-y-6">
        <ConnectAgent />
        <TokenList />
      </div>
    </>
  );
}
