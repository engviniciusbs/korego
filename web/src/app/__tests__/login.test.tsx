import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import type { AuthValue } from "@/lib/auth-context";

const replace = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace, push: vi.fn() }),
}));

const login = vi.fn<AuthValue["login"]>();
let authStatus: AuthValue["status"] = "anonymous";
vi.mock("@/lib/auth-context", () => ({
  useAuth: (): Partial<AuthValue> => ({ status: authStatus, user: null, login }),
}));

import LoginPage from "../login/page";

function submit(code = "123456") {
  fireEvent.change(screen.getByLabelText(/^E-mail/), { target: { value: "admin@exemplo.com" } });
  fireEvent.change(screen.getByLabelText(/^Senha/), { target: { value: "senha-muito-forte" } });
  fireEvent.change(screen.getByLabelText(/^Código/), { target: { value: code } });
  fireEvent.click(screen.getByRole("button", { name: "Entrar" }));
}

beforeEach(() => {
  authStatus = "anonymous";
  replace.mockReset();
  login.mockReset();
});

describe("Login", () => {
  it("mostra a mensagem para INVALID_CREDENTIALS", async () => {
    login.mockRejectedValue(new ApiError("INVALID_CREDENTIALS", "x", 401));
    render(<LoginPage />);
    submit();
    expect(await screen.findByText("E-mail, senha ou código incorretos.")).toBeInTheDocument();
  });

  it("mostra a mensagem para RATE_LIMITED", async () => {
    login.mockRejectedValue(new ApiError("RATE_LIMITED", "x", 429));
    render(<LoginPage />);
    submit();
    expect(
      await screen.findByText("Muitas tentativas. Tente novamente em 15 minutos."),
    ).toBeInTheDocument();
  });

  it("aceita código de recuperação xxxxx-xxxxx e envia os campos", async () => {
    login.mockResolvedValue();
    render(<LoginPage />);
    submit("abcde-12345");
    await waitFor(() =>
      expect(login).toHaveBeenCalledWith("admin@exemplo.com", "senha-muito-forte", "abcde-12345"),
    );
  });

  it("rejeita código em formato inválido sem chamar a API", async () => {
    render(<LoginPage />);
    submit("12ab");
    expect(
      await screen.findByText("Use os 6 dígitos do autenticador ou um código de recuperação."),
    ).toBeInTheDocument();
    expect(login).not.toHaveBeenCalled();
  });

  it("redireciona autenticado para / e setup pendente para /setup/", async () => {
    authStatus = "authenticated";
    const { unmount } = render(<LoginPage />);
    await waitFor(() => expect(replace).toHaveBeenCalledWith("/"));
    unmount();
    authStatus = "setup";
    render(<LoginPage />);
    await waitFor(() => expect(replace).toHaveBeenCalledWith("/setup/"));
  });
});
