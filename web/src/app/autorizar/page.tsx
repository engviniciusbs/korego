"use client";

import { useRouter } from "next/navigation";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { selectCls } from "@/components/domains/form-bits";
import { AuthFrame, Field, describedBy } from "@/components/shell/auth-frame";
import { FullScreenLoading, useAuthRedirect } from "@/components/shell/auth-redirect";
import { Alert, Button, Input } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";
import { useApproveOAuth, useDenyOAuth, useLocationSearch, useOAuthRequest } from "@/lib/oauth";
import { cleanAuthCode, validAuthCode } from "@/lib/validate";

const REQ_RE = /^[A-Za-z0-9_-]{43}$/;
const FRESH_WARNING = "Você acabou de usar um código no login; aguarde o próximo.";
const BAD_REDIRECT = "O endereço de retorno do agente não é válido. Inicie a conexão de novo pelo agente.";

// O navegador só segue retornos http(s); qualquer outro esquema é recusado aqui.
function safeRedirect(redirect: string): boolean {
  try {
    const u = new URL(redirect);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "NETWORK_ERROR":
        return "Não foi possível falar com o servidor. Verifique a conexão.";
      case "RATE_LIMITED":
        return "Muitas tentativas. Tente novamente em 15 minutos.";
      default:
        return err.message;
    }
  }
  return "Erro inesperado. Tente novamente.";
}

export default function AutorizarPage() {
  const { status } = useAuth();
  const router = useRouter();
  const search = useLocationSearch();
  const req = new URLSearchParams(search).get("req") ?? "";
  const validReq = REQ_RE.test(req) ? req : "";

  // Sem sessão: vai para o login e volta para cá (o login valida o `next`).
  const needLogin = status === "anonymous";
  useEffect(() => {
    if (needLogin) {
      router.replace("/login/?next=" + encodeURIComponent(window.location.pathname + window.location.search));
    }
  }, [needLogin, router]);
  const blocked = useAuthRedirect(status, { setup: "/setup/" }) || needLogin;

  const info = useOAuthRequest(status === "authenticated" ? validReq : "");
  const approve = useApproveOAuth(validReq);
  const deny = useDenyOAuth(validReq);
  const ids = useId();
  const [code, setCode] = useState("");
  const [ttl, setTtl] = useState<"30" | "90">("90");
  const [codeError, setCodeError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [wrongCode, setWrongCode] = useState(false);
  const [done, setDone] = useState(false);
  // Trava síncrona: isPending só muda no próximo render, e um segundo envio
  // no mesmo instante passaria pela checagem.
  const inFlight = useRef(false);

  if (blocked) return <FullScreenLoading />;

  const codeId = `${ids}-code`;
  const ttlId = `${ids}-ttl`;
  const busy = approve.isPending || deny.isPending;

  function finish(redirect: string) {
    if (!safeRedirect(redirect)) {
      setFormError(BAD_REDIRECT);
      return;
    }
    setDone(true);
    window.location.assign(redirect);
  }

  async function onApprove(e: FormEvent) {
    e.preventDefault();
    if (busy || inFlight.current) return;
    setFormError(null);
    const clean = cleanAuthCode(code);
    if (!validAuthCode(clean)) {
      setCodeError("Use os 6 dígitos do autenticador ou um código de recuperação.");
      document.getElementById(codeId)?.focus();
      return;
    }
    setCodeError(null);
    inFlight.current = true;
    try {
      const res = await approve.mutateAsync({ code: clean, ttlDays: ttl === "30" ? 30 : 90 });
      finish(res.redirect);
    } catch (err) {
      setWrongCode(err instanceof ApiError && err.code === "INVALID_CODE");
      setFormError(errorMessage(err));
    } finally {
      inFlight.current = false;
    }
  }

  async function onDeny() {
    if (busy || inFlight.current) return;
    setFormError(null);
    inFlight.current = true;
    try {
      const res = await deny.mutateAsync();
      finish(res.redirect);
    } catch (err) {
      setFormError(errorMessage(err));
    } finally {
      inFlight.current = false;
    }
  }

  let body;
  if (!validReq) {
    body = <Alert variant="danger">Pedido de autorização ausente ou inválido. Volte ao agente e tente conectar de novo.</Alert>;
  } else if (info.isPending) {
    body = <FullScreenLoading />;
  } else if (info.isError) {
    body = <Alert variant="danger">{errorMessage(info.error)}</Alert>;
  } else if (done) {
    body = <Alert variant="success">Pronto. Pode voltar ao agente.</Alert>;
  } else {
    const data = info.data;
    body = (
      <form noValidate onSubmit={onApprove} className="space-y-4">
        <div className="space-y-1">
          <p className="text-[13px] text-fg">
            <strong className="font-semibold">{data.clientName}</strong> quer acesso total a este painel.
          </p>
          <p className="text-xs text-fg-subtle">Nome informado pelo próprio aplicativo.</p>
        </div>
        <div className="border border-line-strong bg-surface-2 px-3 py-2.5 text-[13px] text-fg">
          A autorização volta para:{" "}
          <span translate="no" className="font-mono [overflow-wrap:anywhere]">{data.destination}</span>
        </div>
        <Alert variant="warning">O acesso equivale ao do administrador deste servidor.</Alert>
        {(data.sessionFresh || wrongCode) && <Alert variant="info">{FRESH_WARNING}</Alert>}
        {formError && <Alert variant="danger">{formError}</Alert>}
        <Field id={ttlId} label="Validade">
          <select id={ttlId} className={selectCls} value={ttl} onChange={(e) => setTtl(e.target.value === "30" ? "30" : "90")}>
            <option value="90">90 dias</option>
            <option value="30">30 dias</option>
          </select>
        </Field>
        <Field
          id={codeId}
          label="Código 2FA"
          required
          error={codeError ?? undefined}
          hint="6 dígitos do autenticador, ou um código de recuperação."
        >
          <Input
            id={codeId}
            name="code"
            mono
            autoComplete="one-time-code"
            spellCheck={false}
            autoCapitalize="none"
            maxLength={11}
            placeholder="000000"
            className="tracking-[0.2em]"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            invalid={Boolean(codeError)}
            aria-describedby={describedBy(codeId, true, codeError ?? undefined)}
          />
        </Field>
        <div className="flex gap-3">
          <Button type="submit" variant="primary" size="lg" loading={approve.isPending} disabled={busy} className="flex-1">
            Autorizar
          </Button>
          <Button type="button" size="lg" loading={deny.isPending} disabled={busy} onClick={onDeny}>
            Recusar
          </Button>
        </div>
      </form>
    );
  }

  return (
    <AuthFrame eyebrow="Conexão" title="Autorizar agente">
      {body}
    </AuthFrame>
  );
}
