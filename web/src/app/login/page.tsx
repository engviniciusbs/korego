"use client";

import { useId, useState, type FormEvent } from "react";
import { AuthFrame, Field, describedBy } from "@/components/shell/auth-frame";
import { FullScreenLoading, useAuthRedirect } from "@/components/shell/auth-redirect";
import { Alert, Button, Input } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";

const CODE_RE = /^(\d{6}|[a-z0-9]{5}-?[a-z0-9]{5})$/i;

function loginErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "INVALID_CREDENTIALS":
        return "E-mail, senha ou código incorretos.";
      case "RATE_LIMITED":
        return "Muitas tentativas. Tente novamente em 15 minutos.";
      case "SERVER_BUSY":
        return "Servidor ocupado. Tente novamente em instantes.";
      case "VALIDATION_ERROR":
        return err.message;
      case "NETWORK_ERROR":
        return "Não foi possível falar com o servidor. Verifique a conexão.";
    }
  }
  return "Erro inesperado. Tente novamente.";
}

export default function LoginPage() {
  const { status, login } = useAuth();
  const ids = useId();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [codeError, setCodeError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const blocked = useAuthRedirect(status, { authenticated: "/", setup: "/setup/" });
  if (blocked) return <FullScreenLoading />;

  const id = (name: string) => `${ids}-${name}`;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    const clean = code.trim().replace(/\s/g, "");
    if (!CODE_RE.test(clean)) {
      setCodeError("Use os 6 dígitos do autenticador ou um código de recuperação.");
      document.getElementById(id("code"))?.focus();
      return;
    }
    setCodeError(null);
    if (!email.trim() || !password) {
      setFormError("Preencha e-mail e senha.");
      return;
    }
    setBusy(true);
    try {
      await login(email.trim(), password, clean);
      // O redirecionamento acontece quando o status vira "authenticated".
    } catch (err) {
      setFormError(loginErrorMessage(err));
      setBusy(false);
    }
  }

  return (
    <AuthFrame eyebrow="Acesso" title="Entrar no painel">
      <form noValidate onSubmit={submit} className="space-y-4">
        {formError && <Alert variant="danger">{formError}</Alert>}
        <Field id={id("email")} label="E-mail" required>
          <Input
            id={id("email")}
            name="email"
            type="email"
            autoComplete="username"
            spellCheck={false}
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <Field id={id("password")} label="Senha" required>
          <Input
            id={id("password")}
            name="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <Field
          id={id("code")}
          label="Código"
          required
          error={codeError ?? undefined}
          hint={
            <>
              6 dígitos do autenticador, ou um código de recuperação no formato{" "}
              <code translate="no" className="font-mono text-fg-muted">xxxxx-xxxxx</code>.
            </>
          }
        >
          <Input
            id={id("code")}
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
            aria-describedby={describedBy(id("code"), true, codeError ?? undefined)}
          />
        </Field>
        <Button type="submit" variant="primary" size="lg" loading={busy} className="w-full">
          Entrar
        </Button>
      </form>
    </AuthFrame>
  );
}
