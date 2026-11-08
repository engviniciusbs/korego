"use client";

import { useId, useState, type FormEvent } from "react";
import { CertStatus } from "@/components/domains/cert-status";
import { ProxyBanner } from "@/components/domains/proxy-banner";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Button, Dialog, Input, Label, Panel, Spinner } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { usePanelSettings, useSetPanelDomain, useSetPublicPort, type PanelSettings } from "@/lib/settings";
import { validHostname } from "@/lib/validate";

const RECOVERY_CMD = "korego painel --porta-publica";

/** Se a porta 8000 pode ser desligada agora e, se não, por quê. */
function publicPortLock(p: PanelSettings): string {
  if (!p.domain) return "Configure o domínio do painel antes de desligar a porta 8000.";
  if (p.cert.state !== "active") {
    return "Aguarde o certificado do domínio ficar ativo para desligar a porta 8000.";
  }
  if (!p.requestHTTPS || p.requestHost.toLowerCase() !== p.domain.toLowerCase()) {
    return `Para desligar, acesse o painel por https://${p.domain}. Assim você confirma que o domínio funciona.`;
  }
  return "";
}

function DomainForm({ panel }: { panel: PanelSettings }) {
  const uid = useId();
  const inputId = `${uid}-domain`;
  const errId = `${uid}-domain-error`;
  const hintId = `${uid}-domain-hint`;
  const save = useSetPanelDomain();
  const [value, setValue] = useState(panel.domain);
  // O domínio salvo mudou (gravação ou outra aba): acompanha sem perder o aviso.
  const [synced, setSynced] = useState(panel.domain);
  if (synced !== panel.domain) {
    setSynced(panel.domain);
    setValue(panel.domain);
  }
  const [fieldError, setFieldError] = useState("");
  const [notice, setNotice] = useState<{ kind: "ok" | "proxy" | "error"; text: string } | null>(null);

  const locked = !panel.publicPort;
  const host = value.trim().toLowerCase();
  const unchanged = host === panel.domain.toLowerCase();

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (locked || save.isPending) return;
    if (unchanged) {
      setNotice({ kind: "ok", text: host ? "Este já é o domínio do painel." : "O painel já está sem domínio." });
      return;
    }
    setNotice(null);
    if (host !== "" && !validHostname(host)) {
      setFieldError("Hostname inválido. Use letras minúsculas, números e hífens, como painel.exemplo.com.");
      document.getElementById(inputId)?.focus();
      return;
    }
    setFieldError("");
    save.mutate(host, {
      onSuccess: (r) => {
        if ("saved" in r) setNotice({ kind: "proxy", text: `Salvo, mas o proxy não aplicou: ${r.proxyError}` });
        else setNotice({ kind: "ok", text: host ? "Domínio do painel salvo." : "Domínio do painel removido." });
      },
      onError: (err) => {
        if (err instanceof ApiError && err.code === "INVALID_DOMAIN") {
          setFieldError(err.message);
          document.getElementById(inputId)?.focus();
        } else if (err instanceof ApiError && err.code === "DOMAIN_EXISTS") {
          setFieldError("Este domínio já está cadastrado.");
        } else {
          setNotice({ kind: "error", text: err.message });
        }
      },
    });
  };

  const described = [fieldError ? errId : "", hintId].filter(Boolean).join(" ");

  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      <div>
        <Label htmlFor={inputId}>Domínio do painel</Label>
        <div className="mt-1.5 flex flex-col gap-2 sm:flex-row">
          <Input
            id={inputId}
            mono
            value={value}
            placeholder="painel.exemplo.com…"
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="none"
            inputMode="url"
            readOnly={locked}
            invalid={Boolean(fieldError)}
            aria-describedby={described}
            className={cn("sm:max-w-sm", locked && "cursor-not-allowed text-fg-muted")}
            onChange={(e) => {
              setValue(e.target.value);
              setFieldError("");
              setNotice(null);
            }}
          />
          <Button type="submit" variant="primary" loading={save.isPending} disabled={locked}>
            Salvar domínio
          </Button>
        </div>
        {fieldError && (
          <p id={errId} className="mt-1 text-xs text-danger">
            {fieldError}
          </p>
        )}
        <p id={hintId} className="mt-1 text-xs text-fg-subtle">
          {locked
            ? "Reative a porta 8000 pública antes de alterar o domínio."
            : "Aponte um registro A para este servidor. O certificado é emitido automaticamente. Deixe vazio para remover."}
        </p>
      </div>

      {notice?.kind === "ok" && <Alert variant="success">{notice.text}</Alert>}
      {notice?.kind === "proxy" && <Alert variant="warning">{notice.text}</Alert>}
      {notice?.kind === "error" && <Alert variant="danger">{notice.text}</Alert>}
    </form>
  );
}

function CertRow({ panel }: { panel: PanelSettings }) {
  if (!panel.domain) {
    return <p className="text-[13px] text-fg-muted">Sem domínio, o painel só responde pela porta 8000, sem HTTPS.</p>;
  }
  const here = panel.requestHTTPS && panel.requestHost.toLowerCase() === panel.domain.toLowerCase();
  return (
    <dl className="grid gap-x-6 gap-y-2 text-[13px] sm:grid-cols-[auto_1fr]">
      <dt className="text-fg-muted">Certificado</dt>
      <dd className="min-w-0">
        <CertStatus cert={panel.cert} />
      </dd>
      <dt className="text-fg-muted">Endereço</dt>
      <dd className="min-w-0">
        {here ? (
          <span className="font-mono text-fg" translate="no">
            https://{panel.domain} <span className="text-fg-subtle">· você está aqui</span>
          </span>
        ) : (
          <a
            href={`https://${panel.domain}/configuracoes/painel/`}
            translate="no"
            className={cn("font-mono text-accent-text hover:text-fg hover:underline", focusRing)}
          >
            https://{panel.domain}
          </a>
        )}
      </dd>
    </dl>
  );
}

function PublicPort({ panel }: { panel: PanelSettings }) {
  const uid = useId();
  const labelId = `${uid}-label`;
  const descId = `${uid}-desc`;
  const set = useSetPublicPort();
  const [confirming, setConfirming] = useState(false);

  const on = panel.publicPort;
  const lock = on ? publicPortLock(panel) : "";
  const disabled = Boolean(lock) || set.isPending;

  const toggle = () => {
    if (disabled) return;
    set.reset();
    if (on) setConfirming(true);
    else set.mutate(true);
  };

  const confirmOff = () => set.mutate(false, { onSuccess: () => setConfirming(false) });

  return (
    <>
      <div className="flex items-start justify-between gap-6">
        <div className="min-w-0">
          <p id={labelId} className="text-[13px] font-medium text-fg">
            Porta 8000 pública
          </p>
          <p id={descId} className="mt-0.5 text-xs text-fg-muted">
            {lock ||
              (on
                ? `O painel também responde em http://<IP>:8000 (escuta em ${panel.listenAddr}), sem HTTPS.`
                : `A porta 8000 está fechada para a internet; o painel só responde por https://${panel.domain}.`)}
          </p>
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={on}
          aria-labelledby={labelId}
          aria-describedby={descId}
          disabled={disabled}
          aria-busy={set.isPending || undefined}
          onClick={toggle}
          className={cn(
            "relative inline-flex h-6 w-11 shrink-0 touch-manipulation items-center border p-0.5",
            "motion-safe:transition-colors motion-safe:duration-100 disabled:cursor-not-allowed disabled:opacity-50",
            focusRing,
            on ? "border-accent bg-accent/25" : "border-line-strong bg-surface-2",
          )}
        >
          <span
            aria-hidden="true"
            className={cn(
              "block size-4 motion-safe:transition-transform motion-safe:duration-100",
              on ? "translate-x-5 bg-accent" : "translate-x-0 bg-fg-subtle",
            )}
          />
        </button>
      </div>
      {set.isError && !confirming && (
        <Alert variant="danger" className="mt-3">
          {set.error.message}
        </Alert>
      )}

      <Dialog
        open={confirming}
        title="Desligar a porta 8000 pública?"
        confirmLabel="Desligar porta 8000"
        variant="danger"
        loading={set.isPending}
        onConfirm={confirmOff}
        onCancel={() => setConfirming(false)}
      >
        <p>
          O painel deixa de responder em <span className="font-mono text-fg">http://&lt;IP&gt;:8000</span> e passa a
          responder só por <span className="font-mono text-fg" translate="no">https://{panel.domain}</span>.
        </p>
        <p>
          Se o domínio deixar de funcionar, reative pelo servidor com{" "}
          <code translate="no" className="bg-bg px-1 py-0.5 font-mono text-fg">{RECOVERY_CMD}</code>.
        </p>
        {set.isError && <Alert variant="danger">{set.error.message}</Alert>}
      </Dialog>
    </>
  );
}

export default function PainelPage() {
  const q = usePanelSettings();

  return (
    <>
      <PageHeader
        eyebrow="Configurações"
        title="Painel"
        description="Domínio com HTTPS para o painel e acesso pela porta 8000."
      />
      {q.isPending ? (
        <PanelState>
          <Spinner size="sm" decorative />
          <span role="status">Carregando configurações…</span>
        </PanelState>
      ) : q.isError ? (
        <Alert variant="danger" title="Não foi possível carregar as configurações do painel.">
          <p>{q.error.message}</p>
          <Button size="sm" className="mt-2" onClick={() => void q.refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      ) : (
        <div className="space-y-4">
          <ProxyBanner />
          <Panel title="Domínio" description="Endereço HTTPS do painel.">
            <div className="space-y-4">
              <DomainForm panel={q.data} />
              <div className="border-t border-line pt-4">
                <CertRow panel={q.data} />
              </div>
            </div>
          </Panel>
          <Panel title="Acesso direto" description="Porta usada antes de haver domínio.">
            <PublicPort panel={q.data} />
          </Panel>
        </div>
      )}
    </>
  );
}
