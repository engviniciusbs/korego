import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthValue } from "@/lib/auth-context";

const replace = vi.fn();
const push = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace, push }),
}));

vi.mock("qrcode", () => ({
  default: { toDataURL: vi.fn(async () => "data:image/png;base64,AAAA") },
}));

const completeSetup = vi.fn();
let authStatus: AuthValue["status"] = "setup";
vi.mock("@/lib/auth-context", () => ({
  useAuth: (): Partial<AuthValue> => ({ status: authStatus, user: null, completeSetup }),
}));

import SetupPage from "../setup/page";

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const CODES = Array.from({ length: 10 }, (_, i) => `aaaa${i}-bbbb${i}`);

function stubFetch(handler: (url: string, init?: RequestInit) => Response) {
  const f = vi.fn(async (url: string, init?: RequestInit) => handler(url, init));
  vi.stubGlobal("fetch", f);
  return f;
}

function fillStep1({
  token = "tok-123",
  email = "admin@exemplo.com",
  password = "senha-muito-forte",
  confirm = password,
}: { token?: string; email?: string; password?: string; confirm?: string } = {}) {
  fireEvent.change(screen.getByLabelText(/^Token de setup/), { target: { value: token } });
  fireEvent.change(screen.getByLabelText(/^E-mail/), { target: { value: email } });
  fireEvent.change(screen.getByLabelText(/^Senha/), { target: { value: password } });
  fireEvent.change(screen.getByLabelText(/^Confirmar senha/), { target: { value: confirm } });
  fireEvent.click(screen.getByRole("button", { name: "Continuar" }));
}

beforeEach(() => {
  authStatus = "setup";
  replace.mockReset();
  push.mockReset();
  completeSetup.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("Setup", () => {
  it("não avança com senha curta e mostra erro em linha", async () => {
    const f = stubFetch(() => json(500, {}));
    render(<SetupPage />);
    fillStep1({ password: "curta", confirm: "curta" });
    expect(await screen.findByText("A senha deve ter pelo menos 12 caracteres.")).toBeInTheDocument();
    expect(screen.getByLabelText(/^Senha/)).toHaveAttribute("aria-invalid", "true");
    expect(f).not.toHaveBeenCalled();
  });

  it("não avança quando a confirmação difere", async () => {
    const f = stubFetch(() => json(500, {}));
    render(<SetupPage />);
    fillStep1({ password: "senha-muito-forte", confirm: "senha-muito-forta" });
    expect(await screen.findByText("As senhas não conferem.")).toBeInTheDocument();
    expect(f).not.toHaveBeenCalled();
  });

  it("exige o token de setup", async () => {
    const f = stubFetch(() => json(500, {}));
    render(<SetupPage />);
    fillStep1({ token: "" });
    expect(await screen.findByText("Informe o token de setup.")).toBeInTheDocument();
    expect(f).not.toHaveBeenCalled();
  });

  it("mostra a mensagem de SETUP_TOKEN_REQUIRED (403)", async () => {
    stubFetch(() =>
      json(403, {
        error: {
          code: "SETUP_TOKEN_REQUIRED",
          message: "Token de setup inválido. Veja o arquivo setup-token no servidor.",
        },
      }),
    );
    render(<SetupPage />);
    fillStep1();
    expect(
      await screen.findByText("Token de setup inválido. Veja o arquivo setup-token no servidor."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^Token de setup/)).toBeInTheDocument();
  });

  it("avança com senha >= 12 e confirmação igual, enviando o setupToken", async () => {
    const f = stubFetch(() =>
      json(200, { token: "ch", secret: "JBSWY3DPEHPK3PXP", otpauthUrl: "otpauth://totp/x" }),
    );
    render(<SetupPage />);
    fillStep1();
    expect(await screen.findByText("JBSWY3DPEHPK3PXP")).toBeInTheDocument();
    const [url, init] = f.mock.calls[0];
    expect(url).toBe("/api/setup");
    expect(JSON.parse(String(init?.body))).toEqual({
      setupToken: "tok-123",
      email: "admin@exemplo.com",
      password: "senha-muito-forte",
    });
  });

  it("passo 3 bloqueia 'Ir para o painel' até marcar o checkbox", async () => {
    stubFetch((url) =>
      url === "/api/setup"
        ? json(200, { token: "ch", secret: "JBSWY3DPEHPK3PXP", otpauthUrl: "otpauth://totp/x" })
        : json(200, {
            user: { id: 1, email: "admin@exemplo.com", role: "admin", createdAt: "x" },
            csrfToken: "csrf",
            recoveryCodes: CODES,
          }),
    );
    render(<SetupPage />);
    fillStep1();
    const code = await screen.findByLabelText(/^Código do autenticador/);
    fireEvent.change(code, { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirmar" }));

    expect(await screen.findByText(CODES[0])).toBeInTheDocument();
    expect(completeSetup).toHaveBeenCalledTimes(1);
    // Mesmo autenticado, o passo 3 não redireciona sozinho.
    authStatus = "authenticated";

    const go = screen.getByRole("button", { name: "Ir para o painel" });
    expect(go).toBeDisabled();
    fireEvent.click(go);
    expect(push).not.toHaveBeenCalled();

    fireEvent.click(screen.getByLabelText("Guardei meus códigos"));
    expect(go).toBeEnabled();
    fireEvent.click(go);
    await waitFor(() => expect(push).toHaveBeenCalledWith("/"));
  });

  it("código TOTP inválido mostra a mensagem e permanece no passo 2", async () => {
    stubFetch((url) =>
      url === "/api/setup"
        ? json(200, { token: "ch", secret: "S", otpauthUrl: "otpauth://totp/x" })
        : json(400, { error: { code: "INVALID_TOTP", message: "Código do autenticador inválido." } }),
    );
    render(<SetupPage />);
    fillStep1();
    fireEvent.change(await screen.findByLabelText(/^Código do autenticador/), {
      target: { value: "000000" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Confirmar" }));
    expect(await screen.findByText("Código do autenticador inválido.")).toBeInTheDocument();
    expect(completeSetup).not.toHaveBeenCalled();
  });

  it("redireciona para /login/ quando o setup já foi feito", async () => {
    authStatus = "anonymous";
    stubFetch(() => json(500, {}));
    render(<SetupPage />);
    await waitFor(() => expect(replace).toHaveBeenCalledWith("/login/"));
  });
});
