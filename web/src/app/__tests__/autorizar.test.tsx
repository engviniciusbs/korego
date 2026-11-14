import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthValue } from "@/lib/auth-context";

const replace = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace, push: vi.fn() }),
}));

let authStatus: AuthValue["status"] = "authenticated";
vi.mock("@/lib/auth-context", () => ({
  useAuth: (): Partial<AuthValue> => ({ status: authStatus, user: null }),
}));

import AutorizarPage from "../autorizar/page";

const REQ = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const info = (over: Record<string, unknown> = {}) => ({
  clientName: "Claude Code",
  destination: "http://127.0.0.1:5555/cb",
  registeredAt: "2026-11-14T12:00:00Z",
  sessionFresh: false,
  ...over,
});
function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

type Handler = (init?: { method?: string; body?: string }) => Response;
let routes: Record<string, Handler>;
let assign: ReturnType<typeof vi.fn>;

beforeEach(() => {
  authStatus = "authenticated";
  replace.mockReset();
  window.history.replaceState(null, "", "/autorizar/?req=" + REQ);
  routes = { ["GET /api/oauth/requests/" + REQ]: () => json(info()) };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
      const h = routes[`${init?.method ?? "GET"} ${url.split("?")[0]}`];
      return h ? h(init) : json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
    }),
  );
  assign = vi.fn();
  vi.stubGlobal("location", { ...window.location, assign, pathname: window.location.pathname, search: window.location.search });
});
afterEach(() => vi.unstubAllGlobals());

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <AutorizarPage />
    </QueryClientProvider>,
  );
}

describe("Autorizar agente", () => {
  it("mostra o nome do cliente e o destino", async () => {
    renderPage();
    expect(await screen.findByText("Claude Code")).toBeInTheDocument();
    expect(screen.getByText(/A autorização volta para:/)).toHaveTextContent("http://127.0.0.1:5555/cb");
    expect(screen.getByText("Nome informado pelo próprio aplicativo.")).toBeInTheDocument();
    expect(screen.getByText("O acesso equivale ao do administrador deste servidor.")).toBeInTheDocument();
  });

  it("Autorizar envia código e validade e segue o redirect", async () => {
    let body = "";
    routes["POST /api/oauth/requests/" + REQ + "/approve"] = (init) => {
      body = init?.body ?? "";
      return json({ redirect: "http://127.0.0.1:5555/cb?code=x" });
    };
    renderPage();
    await screen.findByText("Claude Code");
    fireEvent.change(screen.getByLabelText(/^Código 2FA/), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Autorizar" }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("http://127.0.0.1:5555/cb?code=x"));
    expect(JSON.parse(body)).toEqual({ code: "123456", ttlDays: 90 });
    expect(await screen.findByText("Pronto. Pode voltar ao agente.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Autorizar" })).not.toBeInTheDocument();
  });

  it("permite escolher 30 dias", async () => {
    let body = "";
    routes["POST /api/oauth/requests/" + REQ + "/approve"] = (init) => {
      body = init?.body ?? "";
      return json({ redirect: "http://127.0.0.1:5555/cb?code=x" });
    };
    renderPage();
    await screen.findByText("Claude Code");
    fireEvent.change(screen.getByLabelText("Validade"), { target: { value: "30" } });
    fireEvent.change(screen.getByLabelText(/^Código 2FA/), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Autorizar" }));
    await waitFor(() => expect(assign).toHaveBeenCalled());
    expect(JSON.parse(body).ttlDays).toBe(30);
  });

  it("código inválido localmente não chama a API", async () => {
    renderPage();
    await screen.findByText("Claude Code");
    fireEvent.change(screen.getByLabelText(/^Código 2FA/), { target: { value: "12" } });
    fireEvent.click(screen.getByRole("button", { name: "Autorizar" }));
    expect(await screen.findByText(/6 dígitos/)).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("INVALID_CODE mostra a mensagem e o aviso de aguardar o próximo código", async () => {
    routes["POST /api/oauth/requests/" + REQ + "/approve"] = () =>
      json({ error: { code: "INVALID_CODE", message: "Código inválido." } }, 403);
    renderPage();
    await screen.findByText("Claude Code");
    fireEvent.change(screen.getByLabelText(/^Código 2FA/), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Autorizar" }));
    expect(await screen.findByText("Código inválido.")).toBeInTheDocument();
    expect(screen.getByText(/aguarde o próximo/)).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("Recusar segue o redirect com access_denied", async () => {
    routes["POST /api/oauth/requests/" + REQ + "/deny"] = () =>
      json({ redirect: "http://127.0.0.1:5555/cb?error=access_denied" });
    renderPage();
    await screen.findByText("Claude Code");
    fireEvent.click(screen.getByRole("button", { name: "Recusar" }));
    await waitFor(() =>
      expect(assign).toHaveBeenCalledWith(expect.stringContaining("error=access_denied")),
    );
    expect(await screen.findByText("Pronto. Pode voltar ao agente.")).toBeInTheDocument();
  });

  it("não segue retorno que não seja http(s)", async () => {
    routes["POST /api/oauth/requests/" + REQ + "/approve"] = () => json({ redirect: "javascript:alert(1)" });
    routes["POST /api/oauth/requests/" + REQ + "/deny"] = () => json({ redirect: "vscode://x/cb?error=access_denied" });
    renderPage();
    await screen.findByText("Claude Code");
    fireEvent.change(screen.getByLabelText(/^Código 2FA/), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Autorizar" }));
    expect(await screen.findByText(/endereço de retorno do agente não é válido/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Recusar" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/deny"), expect.anything()));
    expect(await screen.findByText(/endereço de retorno do agente não é válido/)).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
    expect(screen.queryByText("Pronto. Pode voltar ao agente.")).not.toBeInTheDocument();
  });

  it("envio duplicado enquanto ocupado não repete o pedido", async () => {
    let calls = 0;
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    routes["POST /api/oauth/requests/" + REQ + "/approve"] = () => {
      calls++;
      return json({ redirect: "http://127.0.0.1:5555/cb?code=x" });
    };
    type Fetch = (url: string, init?: { method?: string; body?: string }) => Promise<Response>;
    const base = vi.mocked(fetch as unknown as Fetch);
    const orig = base.getMockImplementation()!;
    base.mockImplementation(async (url, init) => {
      if (url.includes("/approve")) await gate;
      return orig(url, init);
    });
    renderPage();
    await screen.findByText("Claude Code");
    fireEvent.change(screen.getByLabelText(/^Código 2FA/), { target: { value: "123456" } });
    const form = screen.getByRole("button", { name: "Autorizar" }).closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    fireEvent.click(screen.getByRole("button", { name: "Recusar" }));
    release();
    await waitFor(() => expect(assign).toHaveBeenCalledTimes(1));
    expect(calls).toBe(1);
    expect(base.mock.calls.filter(([u]) => String(u).includes("/deny"))).toHaveLength(0);
  });

  it("404 mostra a mensagem do servidor e nenhum botão", async () => {
    routes["GET /api/oauth/requests/" + REQ] = () =>
      json({ error: { code: "OAUTH_REQUEST_NOT_FOUND", message: "Pedido expirado." } }, 404);
    renderPage();
    expect(await screen.findByText("Pedido expirado.")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("sem req mostra erro e nenhum botão", async () => {
    vi.stubGlobal("location", { assign, pathname: "/autorizar/", search: "" });
    renderPage();
    expect(await screen.findByText(/Pedido de autorização ausente/)).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("sessionFresh mostra o aviso", async () => {
    routes["GET /api/oauth/requests/" + REQ] = () => json(info({ sessionFresh: true }));
    renderPage();
    expect(await screen.findByText(/aguarde o próximo/)).toBeInTheDocument();
  });

  it("sem sessão redireciona para o login com next", async () => {
    authStatus = "anonymous";
    renderPage();
    await waitFor(() =>
      expect(replace).toHaveBeenCalledWith(
        "/login/?next=" + encodeURIComponent("/autorizar/?req=" + REQ),
      ),
    );
  });
});
