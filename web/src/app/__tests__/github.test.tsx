import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import GitHubPage from "../(painel)/configuracoes/github/page";

const nav = vi.hoisted(() => ({ search: "", replace: vi.fn() }));

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(nav.search),
  useRouter: () => ({ push: vi.fn(), replace: nav.replace }),
  usePathname: () => "/configuracoes/github/",
}));

class FakeWS {
  readyState = 0;
  onopen: (() => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {}
  send() {}
  close() {
    this.readyState = 3;
    this.onclose?.({ code: 1000 });
  }
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const disconnected = (panelReady: boolean) => ({ connected: false, app: null, installations: [], panelReady });
const connected = (over: Record<string, unknown> = {}) => ({
  connected: true,
  app: {
    slug: "korego-exemplo",
    owner: "exemplo",
    htmlUrl: "https://github.com/apps/korego-exemplo",
    installUrl: "https://github.com/apps/korego-exemplo/installations/new",
  },
  installations: [{ id: 11, account: "exemplo" }],
  panelReady: true,
  ...over,
});

type Handler = (init?: { method?: string; body?: string }) => Response;
let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  nav.search = "";
  nav.replace.mockReset();
  routes = { "GET /api/github": () => json(disconnected(true)) };
  vi.stubGlobal("WebSocket", FakeWS);
  fetchMock = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const h = routes[`${init?.method ?? "GET"} ${url.split("?")[0]}`];
    return h ? h(init) : json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <GitHubPage />
    </QueryClientProvider>,
  );
}

describe("Configurações → GitHub", () => {
  it("GitHub sem painel HTTPS explica e não mostra o botão", async () => {
    routes["GET /api/github"] = () => json(disconnected(false));
    renderPage();
    expect(await screen.findByText(/precisa do painel em HTTPS/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Configurações → Painel/ })).toHaveAttribute(
      "href",
      "/configuracoes/painel/",
    );
    expect(screen.queryByRole("button", { name: /Conectar GitHub/ })).toBeNull();
  });

  it("GitHub mostra erro de state inválido vindo da URL", async () => {
    nav.search = "erro=STATE_INVALID";
    renderPage();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/A conexão expirou ou não começou neste painel/);
    await waitFor(() => expect(nav.replace).toHaveBeenCalledWith("/configuracoes/github/", { scroll: false }));
  });

  it("conectado=1 mostra sucesso", async () => {
    nav.search = "conectado=1";
    routes["GET /api/github"] = () => json(connected());
    renderPage();
    expect(await screen.findByText(/GitHub App criado e conectado/)).toBeInTheDocument();
  });

  it("valida a organização antes de pedir o manifest", async () => {
    renderPage();
    const field = await screen.findByLabelText(/Organização/);
    fireEvent.change(field, { target: { value: "minha org" } });
    fireEvent.click(screen.getByRole("button", { name: "Conectar GitHub" }));
    expect(await screen.findByText(/Use só letras, números e hífens/)).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/github/manifest", expect.anything());
  });

  it("conectado mostra o App, as instalações e desconecta com confirmação", async () => {
    routes["GET /api/github"] = () => json(connected());
    routes["DELETE /api/github"] = () =>
      json({
        removed: true,
        manageUrl: "https://github.com/settings/apps/korego-exemplo",
        message: "As credenciais foram apagadas do KoreGO. Remova também o App no GitHub.",
      });
    renderPage();
    expect(await screen.findByText("korego-exemplo")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Instalar nos repositórios/ })).toHaveAttribute(
      "href",
      "https://github.com/apps/korego-exemplo/installations/new",
    );
    expect(screen.getByRole("link", { name: /Instalar nos repositórios/ })).toHaveAttribute("target", "_blank");
    expect(screen.getByRole("list", { name: "Instalações" })).toHaveTextContent("exemplo");

    fireEvent.click(screen.getByRole("button", { name: "Desconectar" }));
    const dialog = await screen.findByRole("dialog", { name: "Desconectar o GitHub?" });
    expect(dialog).toHaveTextContent(/apague o App no GitHub/);
    routes["GET /api/github"] = () => json(disconnected(true));
    fireEvent.click(within(dialog).getByRole("button", { name: "Desconectar" }));
    const link = await screen.findByRole("link", { name: /Apagar o App no GitHub/ });
    expect(link).toHaveAttribute("href", "https://github.com/settings/apps/korego-exemplo");
  });

  it("mostra o erro das instalações", async () => {
    routes["GET /api/github"] = () =>
      json(connected({ installations: [], installationsError: "Não foi possível listar as instalações do GitHub App." }));
    renderPage();
    expect(await screen.findByText("Não foi possível listar as instalações do GitHub App.")).toBeInTheDocument();
  });
});
