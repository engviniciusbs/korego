"use client";

import { useRouter } from "next/navigation";
import QRCode from "qrcode";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { AuthFrame, Field, describedBy } from "@/components/shell/auth-frame";
import { FullScreenLoading, useAuthRedirect } from "@/components/shell/auth-redirect";
import { Alert, Button, Input } from "@/components/ui";
import { focusRing } from "@/components/ui/focus";
import { ApiError, api } from "@/lib/api";
import { useAuth, type SetupResult } from "@/lib/auth-context";
import { cn } from "@/lib/cn";

const MIN_PASSWORD = 12;
const TOKEN_REQUIRED_MSG = "Token de setup inválido. Veja o arquivo setup-token no servidor.";

type Step = 1 | 2 | 3;

interface Challenge {
  token: string;
  secret: string;
  otpauthUrl: string;
}

interface Step1Errors {
  setupToken?: string;
  email?: string;
  password?: string;
  confirm?: string;
}

const STEPS: Array<{ n: Step; label: string }> = [
  { n: 1, label: "Conta" },
  { n: 2, label: "Autenticador" },
  { n: 3, label: "Recuperação" },
];

function genericMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "RATE_LIMITED":
        return "Muitas tentativas. Tente novamente mais tarde.";
      case "SERVER_BUSY":
        return "Servidor ocupado. Tente novamente em instantes.";
      case "NETWORK_ERROR":
        return "Não foi possível falar com o servidor. Verifique a conexão.";
    }
  }
  return "Erro inesperado. Tente novamente.";
}

function StepIndicator({ step }: { step: Step }) {
  return (
    <ol aria-label="Etapas da configuração" className="flex items-center gap-1">
      {STEPS.map((s) => (
        <li
          key={s.n}
          aria-current={s.n === step ? "step" : undefined}
          className={cn(
            "flex h-5 items-center border px-1.5 font-mono text-[10px] tabular-nums tracking-[0.06em]",
            s.n === step
              ? "border-accent/60 bg-accent/15 text-accent-text"
              : s.n < step
                ? "border-line-strong text-fg-muted"
                : "border-line text-fg-subtle",
          )}
        >
          <span aria-hidden="true">0{s.n}</span>
          <span className="sr-only">
            Passo {s.n} de 3: {s.label}
            {s.n < step ? " (concluído)" : ""}
          </span>
        </li>
      ))}
    </ol>
  );
}

export default function SetupPage() {
  const { status, completeSetup } = useAuth();
  const router = useRouter();
  const ids = useId();

  const [step, setStep] = useState<Step>(1);
  const [setupToken, setSetupToken] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [errors, setErrors] = useState<Step1Errors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [challenge, setChallenge] = useState<Challenge | null>(null);
  const [qr, setQr] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [codeError, setCodeError] = useState<string | null>(null);

  const [codes, setCodes] = useState<string[]>([]);
  const [saved, setSaved] = useState(false);
  const [copyMsg, setCopyMsg] = useState<{ ok: boolean; text: string } | null>(null);

  // Ao trocar de passo, o formulário anterior some: leva o foco ao novo título.
  const firstRender = useRef(true);
  useEffect(() => {
    if (firstRender.current) {
      firstRender.current = false;
      return;
    }
    document.getElementById("auth-title")?.focus();
  }, [step]);

  // Passo 3 sem confirmar: avisa antes de fechar a aba (os códigos não serão exibidos de novo).
  useEffect(() => {
    if (step !== 3 || saved) return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [step, saved]);

  // No passo 3 o usuário já está autenticado, mas precisa guardar os códigos antes de sair.
  const blocked = useAuthRedirect(
    status,
    { authenticated: "/", anonymous: "/login/" },
    step !== 3,
  );
  if (blocked) return <FullScreenLoading />;

  const id = (name: string) => `${ids}-${name}`;

  async function submitAccount(e: FormEvent) {
    e.preventDefault();
    const next: Step1Errors = {};
    if (!setupToken.trim()) next.setupToken = "Informe o token de setup.";
    if (!/^[^\s@]+@[^\s@]+$/.test(email.trim())) next.email = "Informe um e-mail válido.";
    if (password.length < MIN_PASSWORD)
      next.password = `A senha deve ter pelo menos ${MIN_PASSWORD} caracteres.`;
    else if (confirm !== password) next.confirm = "As senhas não conferem.";
    setErrors(next);
    setFormError(null);
    if (Object.keys(next).length > 0) {
      const first = (["setupToken", "email", "password", "confirm"] as const).find((k) => next[k]);
      if (first) document.getElementById(id(first))?.focus();
      return;
    }

    setBusy(true);
    try {
      const ch = await api<Challenge>("/api/setup", {
        method: "POST",
        body: { setupToken: setupToken.trim(), email: email.trim(), password },
      });
      let dataUrl: string | null = null;
      try {
        dataUrl = await QRCode.toDataURL(ch.otpauthUrl, { margin: 1, width: 192 });
      } catch {
        dataUrl = null;
      }
      setChallenge(ch);
      setQr(dataUrl);
      setCode("");
      setCodeError(null);
      setStep(2);
    } catch (err) {
      if (err instanceof ApiError) {
        switch (err.code) {
          case "SETUP_ALREADY_DONE":
            router.replace("/login/");
            return;
          case "SETUP_TOKEN_REQUIRED":
            setErrors({ setupToken: TOKEN_REQUIRED_MSG });
            document.getElementById(id("setupToken"))?.focus();
            return;
          case "WEAK_PASSWORD":
            setErrors({ password: err.message });
            return;
          case "VALIDATION_ERROR":
            setFormError(err.message);
            return;
        }
      }
      setFormError(genericMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function submitCode(e: FormEvent) {
    e.preventDefault();
    if (!challenge) return;
    const clean = code.replace(/\s/g, "");
    if (!/^\d{6}$/.test(clean)) {
      setCodeError("Informe os 6 dígitos do autenticador.");
      document.getElementById(id("code"))?.focus();
      return;
    }
    setCodeError(null);
    setFormError(null);
    setBusy(true);
    try {
      const result = await api<SetupResult>("/api/setup/confirm", {
        method: "POST",
        body: { token: challenge.token, code: clean },
      });
      setCodes(result.recoveryCodes);
      setCopyMsg(null);
      setStep(3);
      completeSetup(result);
    } catch (err) {
      if (err instanceof ApiError) {
        switch (err.code) {
          case "INVALID_TOTP":
            setCodeError("Código do autenticador inválido.");
            document.getElementById(id("code"))?.focus();
            return;
          case "SETUP_ALREADY_DONE":
            router.replace("/login/");
            return;
          case "SETUP_TOKEN_INVALID":
            setChallenge(null);
            setQr(null);
            setStep(1);
            setFormError("A configuração expirou. Envie os dados novamente para gerar um novo QR code.");
            return;
        }
      }
      setFormError(genericMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function copy(text: string, okText: string) {
    try {
      if (!navigator.clipboard) throw new Error("sem clipboard");
      await navigator.clipboard.writeText(text);
      setCopyMsg({ ok: true, text: okText });
    } catch {
      setCopyMsg({
        ok: false,
        text: "Não foi possível copiar. Selecione o texto e copie manualmente.",
      });
    }
  }

  function download() {
    const body = [
      "KoreGO: códigos de recuperação",
      "Cada código funciona uma única vez.",
      "",
      ...codes,
      "",
    ].join("\n");
    const url = URL.createObjectURL(new Blob([body], { type: "text/plain;charset=utf-8" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = "korego-codigos-de-recuperacao.txt";
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  if (step === 1) {
    return (
      <AuthFrame eyebrow="Configuração inicial" title="Crie a conta de administrador" aside={<StepIndicator step={1} />}>
        <form noValidate onSubmit={submitAccount} className="space-y-4">
          {formError && <Alert variant="danger">{formError}</Alert>}
          <Field
            id={id("setupToken")}
            label="Token de setup"
            required
            error={errors.setupToken}
            hint={
              <>
                O token está no arquivo <code translate="no" className="font-mono text-fg-muted">/etc/korego/setup-token</code> do
                servidor (ou no log do serviço: <code translate="no" className="font-mono text-fg-muted">journalctl -u korego</code>)
              </>
            }
          >
            <Input
              id={id("setupToken")}
              name="setupToken"
              mono
              autoComplete="off"
              spellCheck={false}
              value={setupToken}
              onChange={(e) => setSetupToken(e.target.value)}
              invalid={Boolean(errors.setupToken)}
              aria-describedby={describedBy(id("setupToken"), true, errors.setupToken)}
            />
          </Field>
          <Field id={id("email")} label="E-mail" required error={errors.email}>
            <Input
              id={id("email")}
              name="email"
              type="email"
              autoComplete="username"
              spellCheck={false}
              placeholder="admin@exemplo.com…"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              invalid={Boolean(errors.email)}
              aria-describedby={describedBy(id("email"), false, errors.email)}
            />
          </Field>
          <Field
            id={id("password")}
            label="Senha"
            required
            error={errors.password}
            hint={`Mínimo de ${MIN_PASSWORD} caracteres.`}
          >
            <Input
              id={id("password")}
              name="password"
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              invalid={Boolean(errors.password)}
              aria-describedby={describedBy(id("password"), true, errors.password)}
            />
          </Field>
          <Field id={id("confirm")} label="Confirmar senha" required error={errors.confirm}>
            <Input
              id={id("confirm")}
              name="confirm"
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              invalid={Boolean(errors.confirm)}
              aria-describedby={describedBy(id("confirm"), false, errors.confirm)}
            />
          </Field>
          <div className="flex justify-end pt-1">
            <Button type="submit" variant="primary" size="lg" loading={busy}>
              Continuar
            </Button>
          </div>
        </form>
      </AuthFrame>
    );
  }

  if (step === 2 && challenge) {
    return (
      <AuthFrame
        wide
        eyebrow="Configuração inicial"
        title="Ative a verificação em duas etapas"
        aside={<StepIndicator step={2} />}
      >
        <form noValidate onSubmit={submitCode} className="space-y-5">
          {formError && <Alert variant="danger">{formError}</Alert>}
          <div className="flex flex-col gap-5 sm:flex-row">
            <div className="flex size-[200px] shrink-0 items-center justify-center border border-line-strong bg-white p-1">
              {qr ? (
                // eslint-disable-next-line @next/next/no-img-element -- data URL gerada no cliente
                <img
                  src={qr}
                  width={192}
                  height={192}
                  alt="QR code para adicionar o KoreGO ao aplicativo autenticador"
                />
              ) : (
                <p className="p-3 text-center text-xs text-black">QR code indisponível. Use o segredo ao lado.</p>
              )}
            </div>
            <div className="min-w-0 space-y-3 text-[13px] text-fg-muted">
              <p>
                Escaneie o QR code com um aplicativo autenticador (Google Authenticator, 1Password,
                Aegis…) ou digite o segredo manualmente.
              </p>
              <div className="space-y-1.5">
                <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-fg-muted">Segredo</p>
                <div className="flex items-stretch gap-2">
                  <code
                    translate="no"
                    className="min-w-0 flex-1 border border-line bg-bg px-2.5 py-1.5 font-mono text-xs tracking-wider text-fg break-all select-all"
                  >
                    {challenge.secret}
                  </code>
                  <Button size="sm" className="h-auto" onClick={() => copy(challenge.secret, "Segredo copiado.")}>
                    Copiar
                  </Button>
                </div>
                <p aria-live="polite" className={cn("min-h-4 text-xs", copyMsg?.ok ? "text-success" : "text-danger")}>
                  {copyMsg?.text}
                </p>
              </div>
            </div>
          </div>
          <Field
            id={id("code")}
            label="Código do autenticador"
            required
            error={codeError ?? undefined}
            hint="Os 6 dígitos exibidos no aplicativo."
          >
            <Input
              id={id("code")}
              name="code"
              mono
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={7}
              spellCheck={false}
              placeholder="000000"
              className="max-w-40 tracking-[0.3em]"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              invalid={Boolean(codeError)}
              aria-describedby={describedBy(id("code"), true, codeError ?? undefined)}
            />
          </Field>
          <div className="flex justify-end gap-2 pt-1">
            <Button type="submit" variant="primary" size="lg" loading={busy}>
              Confirmar
            </Button>
          </div>
        </form>
      </AuthFrame>
    );
  }

  return (
    <AuthFrame wide eyebrow="Configuração inicial" title="Guarde os códigos de recuperação" aside={<StepIndicator step={3} />}>
      <div className="space-y-4">
        <Alert variant="warning" title="Estes códigos aparecem só agora.">
          Use um deles para entrar se perder o acesso ao autenticador. Cada código funciona uma única vez.
        </Alert>
        <ol
          aria-label="Códigos de recuperação"
          translate="no"
          className="grid grid-cols-2 gap-px border border-line bg-line font-mono text-sm tabular-nums"
        >
          {codes.map((c, i) => (
            <li key={c} className="flex items-center gap-3 bg-bg px-3 py-2 select-all">
              <span aria-hidden="true" className="w-5 text-[11px] text-fg-subtle">
                {String(i + 1).padStart(2, "0")}
              </span>
              <span className="text-fg">{c}</span>
            </li>
          ))}
        </ol>
        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={() => copy(codes.join("\n"), "Códigos copiados.")}>Copiar</Button>
          <Button onClick={download}>Baixar .txt</Button>
          <p aria-live="polite" className={cn("text-xs", copyMsg?.ok ? "text-success" : "text-danger")}>
            {copyMsg?.text}
          </p>
        </div>
        <div className="flex flex-col gap-3 border-t border-line pt-4 sm:flex-row sm:items-center sm:justify-between">
          <label className="inline-flex cursor-pointer items-center gap-2.5 text-[13px] text-fg">
            <input
              type="checkbox"
              checked={saved}
              onChange={(e) => setSaved(e.target.checked)}
              className={cn("size-4 cursor-pointer accent-[var(--color-accent)]", focusRing)}
            />
            Guardei meus códigos
          </label>
          <Button variant="primary" size="lg" disabled={!saved} onClick={() => router.push("/")}>
            Ir para o painel
          </Button>
        </div>
      </div>
    </AuthFrame>
  );
}
