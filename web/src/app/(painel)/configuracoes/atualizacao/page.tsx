"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Button, Panel, Spinner } from "@/components/ui";
import { api } from "@/lib/api";

interface UpdateInfo {
  current: string;
  latest: string | null;
  available: boolean;
  error?: string;
}

const POLL_INTERVAL_MS = 2000;
const POLL_TIMEOUT_MS = 60_000;

type Phase = "idle" | "confirming" | "updating" | "done" | "timeout";

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 border-b border-line py-2.5 last:border-b-0">
      <dt className="text-[13px] text-fg-muted">{label}</dt>
      <dd className="font-mono text-[13px] text-fg" translate="no">
        {children}
      </dd>
    </div>
  );
}

export default function AtualizacaoPage() {
  const info = useQuery({ queryKey: ["update"], queryFn: () => api<UpdateInfo>("/api/update") });
  const [phase, setPhase] = useState<Phase>("idle");
  const [newVersion, setNewVersion] = useState("");
  const startedFrom = useRef("");

  const start = useMutation({
    mutationFn: () => api<unknown>("/api/update", { method: "POST" }),
    onSuccess: () => {
      startedFrom.current = info.data?.current ?? "";
      setPhase("updating");
    },
    onError: () => setPhase("idle"),
  });

  // Após o 202: o serviço reinicia; consulta /api/health até a versão mudar.
  useEffect(() => {
    if (phase !== "updating") return;
    const deadline = Date.now() + POLL_TIMEOUT_MS;
    let stopped = false;
    const tick = async () => {
      if (stopped) return;
      try {
        const res = await fetch("/api/health", { cache: "no-store" });
        if (res.ok) {
          const body = (await res.json()) as { version?: string };
          if (body.version && body.version !== startedFrom.current) {
            stopped = true;
            setNewVersion(body.version);
            setPhase("done");
            return;
          }
        }
      } catch {
        // painel reiniciando: tenta de novo
      }
      if (!stopped && Date.now() >= deadline) {
        stopped = true;
        setPhase("timeout");
      }
    };
    const id = setInterval(() => void tick(), POLL_INTERVAL_MS);
    return () => {
      stopped = true;
      clearInterval(id);
    };
  }, [phase]);

  const d = info.data;

  return (
    <>
      <PageHeader
        eyebrow="Configurações"
        title="Atualização"
        description="Verifique e instale novas versões do KoreGO."
      />
      <Panel title="Versão">
        {info.isPending ? (
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">Verificando atualizações…</span>
          </PanelState>
        ) : info.isError ? (
          <Alert variant="danger" title="Não foi possível consultar o servidor.">
            <p>{info.error.message}</p>
            <Button size="sm" className="mt-2" onClick={() => void info.refetch()}>
              Tentar novamente
            </Button>
          </Alert>
        ) : (
          <div className="space-y-4">
            <dl>
              <Row label="Versão atual">{d?.current}</Row>
              <Row label="Última versão">{d?.latest ?? "—"}</Row>
            </dl>

            {d?.current === "dev" && (
              <Alert variant="info">Builds de desenvolvimento (dev) não se atualizam automaticamente.</Alert>
            )}
            {d?.error && (
              <Alert variant="danger" title="Não foi possível verificar a última versão.">
                <p>{d.error}</p>
              </Alert>
            )}
            {!d?.error && d?.current !== "dev" && !d?.available && phase === "idle" && (
              <p className="text-[13px] text-fg-muted">O KoreGO já está na versão mais recente.</p>
            )}
            {start.isError && (
              <Alert variant="danger" title="Não foi possível atualizar.">
                <p>{start.error.message}</p>
              </Alert>
            )}

            {phase === "updating" && (
              <PanelState>
                <Spinner size="sm" decorative />
                <span role="status">Atualizando… o painel vai reconectar.</span>
              </PanelState>
            )}
            {phase === "done" && (
              <Alert variant="success" title="Atualização concluída.">
                <p>O KoreGO agora está na versão {newVersion}.</p>
                <Button size="sm" className="mt-2" onClick={() => window.location.reload()}>
                  Recarregar painel
                </Button>
              </Alert>
            )}
            {phase === "timeout" && (
              <Alert variant="warning" title="O painel não voltou com a nova versão a tempo.">
                <p>
                  Verifique o serviço no servidor com <code className="font-mono">journalctl -u korego</code>. Se a nova
                  versão falhar, o KoreGO restaura a anterior automaticamente.
                </p>
              </Alert>
            )}

            {d?.available && d.latest && (phase === "idle" || phase === "confirming") && (
              <div className="flex flex-wrap items-center gap-2">
                {phase === "confirming" ? (
                  <>
                    <span className="text-[13px] text-fg-muted">
                      O painel ficará indisponível por alguns segundos. Continuar?
                    </span>
                    <Button size="sm" variant="danger" autoFocus loading={start.isPending} onClick={() => start.mutate()}>
                      Confirmar atualização
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => setPhase("idle")}>
                      Cancelar
                    </Button>
                  </>
                ) : (
                  <Button
                    onClick={() => {
                      start.reset();
                      setPhase("confirming");
                    }}
                  >
                    Atualizar para {d.latest}
                  </Button>
                )}
              </div>
            )}
          </div>
        )}
      </Panel>
    </>
  );
}
