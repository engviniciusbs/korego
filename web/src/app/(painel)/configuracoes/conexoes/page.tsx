"use client";

import Link from "next/link";
import { useId, useState, type FormEvent } from "react";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { AgentGrid, CodeCopy, CopyButton } from "@/components/connections/agent-grid";
import { Alert, Badge, Button, Dialog, Input, Label, Panel, Spinner } from "@/components/ui";
import { oauthAgentClients, tokenAgentClients } from "@/lib/mcp-clients";
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

/** Conexão por OAuth: o agente recebe só a URL e o navegador pede a autorização. */
function ConnectAgent() {
  const panel = usePanelSettings();
  const ready = panel.data && panel.data.domain && panel.data.cert?.state === "active";
  const url = ready ? `https://${panel.data.domain}/mcp` : "";
  return (
    <Panel
      title="Conectar um agente"
      description="Escolha onde usar o KoreGO. O agente abre o navegador nesta tela de autorização e você confirma com o código 2FA."
    >
      {panel.isPending ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando…</span>
        </PanelState>
      ) : panel.isError ? (
        <Alert variant="danger">Não foi possível carregar a configuração do painel.</Alert>
      ) : ready ? (
        <div className="-m-4">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line px-4 py-3 sm:px-6">
            <span className="font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle">Servidor</span>
            <div className="min-w-0 flex-1 sm:max-w-xl">
              <CodeCopy text={url} label="Copiar URL do servidor" />
            </div>
          </div>
          <AgentGrid clients={oauthAgentClients(`https://${panel.data.domain}`)} />
          <p className="border-t border-line px-4 py-3 text-[12.5px] text-fg-muted sm:px-6">
            Outro cliente com OAuth: informe só a URL do servidor. Sem OAuth, crie um token em Agentes conectados.
          </p>
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

/** Origem canônica do painel (domínio com HTTPS), ou a da página como alternativa. */
function usePanelOrigin(): string {
  const panel = usePanelSettings();
  const d = panel.data;
  return d?.domain && d.cert?.state === "active" ? `https://${d.domain}` : location.origin;
}

function CreateDialog({ onClose }: { onClose: () => void }) {
  const uid = useId();
  const origin = usePanelOrigin();
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
        eyebrow="Conexões"
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
        <div>
          <Label>Conectar um agente</Label>
          <AgentGrid compact className="mt-1.5 border border-line" clients={tokenAgentClients(origin, created.token)} />
        </div>
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
      eyebrow="Conexões"
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
      title="Agentes conectados"
      description="Agentes autorizados pelo navegador e tokens criados à mão. Revogar bloqueia na próxima chamada."
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
          <span role="status">Carregando conexões…</span>
        </PanelState>
      ) : tokens.isError ? (
        <Alert variant="danger" title="Não foi possível carregar as conexões.">
          <p>{tokens.error.message}</p>
          <Button size="sm" className="mt-2" onClick={() => void tokens.refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      ) : items.length === 0 ? (
        <PanelState>Nenhum agente conectado.</PanelState>
      ) : (
        <div className="relative -m-4 overflow-x-auto">
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

export default function ConexoesPage() {
  return (
    <>
      <PageHeader
        eyebrow="Configurações"
        title="Conexões"
        description="Conecte agentes de IA ao painel pelo MCP. Cada conexão vale como o administrador deste servidor."
      />
      <div className="space-y-6">
        <ConnectAgent />
        <TokenList />
      </div>
    </>
  );
}
