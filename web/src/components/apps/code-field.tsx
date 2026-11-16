"use client";

import { forwardRef } from "react";
import { Field, describedBy } from "@/components/shell/auth-frame";
import { Input } from "@/components/ui";
import { ApiError } from "@/lib/api";

/** Erros da API que pertencem ao campo do código 2FA. */
export const isCodeError = (err: unknown) =>
  err instanceof ApiError && ["CODE_REQUIRED", "INVALID_CODE", "RATE_LIMITED"].includes(err.code);

export const CODE_FORMAT_ERROR = "Use os 6 dígitos do autenticador ou um código de recuperação.";

export interface CodeFieldProps {
  id: string;
  value: string;
  onChange: (v: string) => void;
  error?: string | null;
}

/** Campo do código de confirmação (2FA), como no diálogo de troca do proxy. */
export const CodeField = forwardRef<HTMLInputElement, CodeFieldProps>(function CodeField(
  { id, value, onChange, error },
  ref,
) {
  return (
    <Field
      id={id}
      label="Código de confirmação"
      required
      error={error ?? undefined}
      hint={
        <>
          6 dígitos do autenticador, ou um código de recuperação no formato{" "}
          <code translate="no" className="font-mono text-fg-muted">
            xxxxx-xxxxx
          </code>
          .
        </>
      }
    >
      <Input
        ref={ref}
        id={id}
        name="code"
        mono
        autoComplete="one-time-code"
        spellCheck={false}
        autoCapitalize="none"
        maxLength={11}
        placeholder="000000"
        className="max-w-48 tracking-[0.2em]"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        invalid={Boolean(error)}
        aria-describedby={describedBy(id, true, error ?? undefined)}
      />
    </Field>
  );
});
