"use client";

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Badge, Button, Panel, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";
import { auditDetail, type AuditItem } from "@/lib/audit";
import { describeUserAgent, formatDateTime } from "@/lib/format";

interface SessionItem {
  id: string;
  ip: string;
  userAgent: string;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
  current: boolean;
}

interface AuditPage {
  items: AuditItem[];
  nextBefore: number | null;
}

const AUDIT_PAGE = 50;

const actionLabels: Record<string, { label: string; tone: "neutral" | "success" | "danger" | "warning" }> = {
  "auth.setup": { label: "Configuração inicial", tone: "success" },
  "auth.login": { label: "Login", tone: "success" },
  "auth.login_failed": { label: "Falha de login", tone: "danger" },
  "auth.logout": { label: "Logout", tone: "neutral" },
  "auth.session_revoked": { label: "Sessão revogada", tone: "warning" },
  "system.update": { label: "Atualização do sistema", tone: "warning" },
  "api_token.create": { label: "Token criado", tone: "success" },
  "api_token.revoke": { label: "Token revogado", tone: "warning" },
};

const th = "px-4 py-2 text-left font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-fg-subtle";
const td = "px-4 py-2.5 align-middle";

function Sessions() {
  const qc = useQueryClient();
  const { refresh } = useAuth();
  const [confirming, setConfirming] = useState<string | null>(null);
  const sessions = useQuery({
    queryKey: ["sessions"],
    queryFn: () => api<{ items: SessionItem[] }>("/api/auth/sessions"),
  });
  const revoke = useMutation({
    mutationFn: (s: SessionItem) => api<void>(`/api/auth/sessions/${encodeURIComponent(s.id)}`, { method: "DELETE" }),
    onSuccess: async (_, s) => {
      setConfirming(null);
      if (s.current) {
        // O servidor apagou o cookie; refresh() leva o status a "anonymous" e o guard vai ao login.
        await refresh();
        return;
      }
      await qc.invalidateQueries({ queryKey: ["sessions"] });
      await qc.invalidateQueries({ queryKey: ["audit"] });
    },
  });

  const items = sessions.data?.items ?? [];

  return (
    <Panel
      title="Sessões ativas"
      description="Dispositivos conectados à sua conta. Revogar encerra a sessão imediatamente."
    >
      {revoke.isError && (
        <Alert variant="danger" className="mb-3">
          Não foi possível revogar a sessão: {revoke.error.message}
        </Alert>
      )}
      {sessions.isPending ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando sessões…</span>
        </PanelState>
      ) : sessions.isError ? (
        <Alert variant="danger" title="Não foi possível carregar as sessões.">
          <p>{sessions.error.message}</p>
          <Button size="sm" className="mt-2" onClick={() => void sessions.refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      ) : items.length === 0 ? (
        <PanelState>Nenhuma sessão ativa.</PanelState>
      ) : (
        <div className="-m-4 overflow-x-auto">
          <table className="w-full min-w-[640px] border-collapse text-[13px]">
            <thead className="border-b border-line">
              <tr>
                <th scope="col" className={th}>Navegador</th>
                <th scope="col" className={th}>IP</th>
                <th scope="col" className={th}>Último acesso</th>
                <th scope="col" className={th}>Criada em</th>
                <th scope="col" className={`${th} text-right`}>
                  <span className="sr-only">Ações</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((s) => (
                <tr key={s.id} className="border-b border-line last:border-b-0 hover:bg-surface-2/60">
                  <td className={td}>
                    <div className="flex items-center gap-2">
                      <span className="text-fg" title={s.userAgent}>
                        {describeUserAgent(s.userAgent)}
                      </span>
                      {s.current && (
                        <Badge size="sm" variant="accent">
                          Esta sessão
                        </Badge>
                      )}
                    </div>
                  </td>
                  <td className={`${td} font-mono text-fg-muted`} translate="no">
                    {s.ip || "—"}
                  </td>
                  <td className={`${td} tabular-nums text-fg-muted`}>{formatDateTime(s.lastSeenAt)}</td>
                  <td className={`${td} tabular-nums text-fg-muted`}>{formatDateTime(s.createdAt)}</td>
                  <td className={`${td} text-right`}>
                    {confirming === s.id ? (
                      <div className="inline-flex items-center gap-2">
                        <span className="text-xs text-fg-muted">
                          {s.current ? "Você será desconectado." : "Revogar esta sessão?"}
                        </span>
                        <Button
                          size="sm"
                          variant="danger"
                          autoFocus
                          loading={revoke.isPending}
                          onClick={() => revoke.mutate(s)}
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
                        aria-label={`Revogar sessão ${describeUserAgent(s.userAgent)} (${s.ip || "IP desconhecido"})`}
                        onClick={() => {
                          revoke.reset();
                          setConfirming(s.id);
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
    </Panel>
  );
}

function Audit() {
  const audit = useInfiniteQuery({
    queryKey: ["audit"],
    queryFn: ({ pageParam }) =>
      api<AuditPage>(`/api/audit?limit=${AUDIT_PAGE}${pageParam ? `&before=${pageParam}` : ""}`),
    initialPageParam: 0,
    getNextPageParam: (last) => last.nextBefore ?? undefined,
  });
  const items = audit.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <Panel title="Auditoria" description="Eventos de segurança mais recentes.">
      {audit.isPending ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando eventos…</span>
        </PanelState>
      ) : audit.isError && items.length === 0 ? (
        <Alert variant="danger" title="Não foi possível carregar a auditoria.">
          <p>{audit.error.message}</p>
          <Button size="sm" className="mt-2" onClick={() => void audit.refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      ) : items.length === 0 ? (
        <PanelState>Nenhum evento registrado.</PanelState>
      ) : (
        <>
          <div className="-mx-4 -mt-4 overflow-x-auto">
            <table className="w-full min-w-[640px] border-collapse text-[13px]">
              <thead className="border-b border-line">
                <tr>
                  <th scope="col" className={th}>Data</th>
                  <th scope="col" className={th}>Evento</th>
                  <th scope="col" className={th}>IP</th>
                  <th scope="col" className={th}>Detalhe</th>
                </tr>
              </thead>
              <tbody>
                {items.map((a) => {
                  const known = actionLabels[a.action];
                  return (
                    <tr key={a.id} className="border-b border-line hover:bg-surface-2/60">
                      <td className={`${td} whitespace-nowrap tabular-nums text-fg-muted`}>
                        {formatDateTime(a.createdAt)}
                      </td>
                      <td className={td}>
                        {known ? (
                          <Badge size="sm" variant={known.tone} dot>
                            {known.label}
                          </Badge>
                        ) : (
                          <span className="font-mono text-xs text-fg-muted">{a.action}</span>
                        )}
                      </td>
                      <td className={`${td} font-mono text-fg-muted`} translate="no">
                        {a.ip || "—"}
                      </td>
                      <td className={`${td} max-w-64 truncate text-fg-muted`} title={auditDetail(a)}>
                        {auditDetail(a)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {audit.isFetchNextPageError && (
            <Alert variant="danger" className="mt-3">
              Não foi possível carregar mais eventos: {audit.error?.message}
            </Alert>
          )}
          <div className="flex items-center justify-between gap-3 pt-3">
            <p className="text-xs text-fg-subtle tabular-nums">{items.length} {items.length === 1 ? "evento exibido" : "eventos exibidos"}</p>
            {audit.hasNextPage && (
              <Button size="sm" loading={audit.isFetchingNextPage} onClick={() => void audit.fetchNextPage()}>
                Carregar mais
              </Button>
            )}
          </div>
        </>
      )}
    </Panel>
  );
}

export default function SegurancaPage() {
  return (
    <>
      <PageHeader eyebrow="Configurações" title="Segurança" description="Sessões ativas e registro de auditoria." />
      <div className="space-y-6">
        <Sessions />
        <Audit />
      </div>
    </>
  );
}
