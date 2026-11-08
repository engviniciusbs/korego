import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AppDetailPage from "../(painel)/aplicacoes/detalhe/page";
import ProjectDetailPage from "../(painel)/projetos/detalhe/page";
import ProjetosPage from "../(painel)/projetos/page";

const nav = vi.hoisted(() => ({ search: "", push: vi.fn() }));

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(nav.search),
  useRouter: () => ({ push: nav.push, replace: nav.push }),
  usePathname: () => "/",
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

const project = (over: Record<string, unknown>) => ({
  id: 1,
  name: "Loja",
  description: "Site da loja",
  appCount: 0,
  createdAt: "2026-11-08T12:00:00Z",
  ...over,
});

const app = (over: Record<string, unknown> = {}) => ({
  id: 5,
  projectId: 1,
  slug: "loja-web",
  sourceType: "image",
  repo: "",
  image: "nginx:1-alpine",
  buildType: "image",
  branch: "",
  dockerfilePath: "",
  composePath: "",
  composeService: "",
  internalPort: 80,
  healthTimeoutS: 60,
  autoDeploy: false,
  hostPort: 20001,
  currentDeploy: { id: 3, status: "running", commitSha: "0123456789abcdef", finishedAt: "2026-11-08T12:05:00Z" },
  domains: ["loja.exemplo.com"],
  url: "http://127.0.0.1:20001",
  createdAt: "2026-11-08T12:00:00Z",
  updatedAt: "2026-11-08T12:00:00Z",
  ...over,
});

type Handler = (init?: { method?: string; body?: string }) => Response;

let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  nav.search = "";
  nav.push.mockReset();
  routes = {
    "GET /api/projects": () => json({ projects: [project({ appCount: 1 }), project({ id: 2, name: "Vazio", description: "" })] }),
    "GET /api/apps": () => json({ apps: [app()] }),
    "GET /api/apps/5": () => json({ app: app() }),
    "GET /api/apps/5/env": () => json({ names: [] }),
    "GET /api/github": () => json({ connected: false, app: null, installations: [], panelReady: true }),
  };
  vi.stubGlobal("WebSocket", FakeWS);
  fetchMock = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const key = `${init?.method ?? "GET"} ${url.split("?")[0]}`;
    const h = routes[key];
    return h ? h(init) : json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderPage(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe("Projetos", () => {
  it("lista projetos com a contagem e só remove quando vazio", async () => {
    routes["DELETE /api/projects/2"] = () => new Response(null, { status: 204 });
    renderPage(<ProjetosPage />);

    const loja = (await screen.findByRole("link", { name: "Loja" })).closest("li") as HTMLElement;
    expect(within(loja).getByText("1 aplicação")).toBeInTheDocument();
    expect(within(loja).getByText("Site da loja")).toBeInTheDocument();
    expect(within(loja).getByRole("button", { name: /Remover/ })).toBeDisabled();
    expect(within(loja).getByText(/Remova as aplicações do projeto antes/)).toBeInTheDocument();

    const vazio = screen.getByRole("link", { name: "Vazio" }).closest("li") as HTMLElement;
    expect(within(vazio).getByText("0 aplicações")).toBeInTheDocument();
    fireEvent.click(within(vazio).getByRole("button", { name: /Remover/ }));
    const dialog = screen.getByRole("dialog", { name: "Remover projeto?" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Remover" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/projects/2", expect.objectContaining({ method: "DELETE" })),
    );
  });

  it("novo projeto pelo diálogo", async () => {
    let body: unknown;
    routes["POST /api/projects"] = (init) => {
      body = JSON.parse(init?.body ?? "{}");
      return json({ project: project({ id: 3, name: "Blog" }) }, 201);
    };
    renderPage(<ProjetosPage />);
    await screen.findByRole("link", { name: "Loja" });
    fireEvent.click(screen.getByRole("button", { name: "Novo projeto" }));
    const dialog = screen.getByRole("dialog", { name: "Novo projeto" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Criar projeto" }));
    expect(await within(dialog).findByText(/Informe o nome/)).toBeInTheDocument();
    fireEvent.change(within(dialog).getByLabelText(/^Nome/), { target: { value: "Blog" } });
    fireEvent.change(within(dialog).getByLabelText(/^Descrição/), { target: { value: "Notícias" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Criar projeto" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(body).toEqual({ name: "Blog", description: "Notícias" });
  });

  it("detalhe do projeto lista as aplicações com status, domínios e deploy", async () => {
    nav.search = "id=1";
    renderPage(<ProjectDetailPage />);
    expect(await screen.findByRole("heading", { level: 1, name: "Loja" })).toBeInTheDocument();
    const row = (await screen.findByRole("link", { name: "loja-web" })).closest("li") as HTMLElement;
    expect(within(row).getByText("No ar")).toBeInTheDocument();
    expect(within(row).getByText("loja.exemplo.com")).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: /Deploy/ })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Nova aplicação" })).toHaveAttribute(
      "href",
      "/aplicacoes/nova/?projeto=1",
    );
  });

  it("projeto inexistente mostra não encontrado", async () => {
    nav.search = "id=99";
    renderPage(<ProjectDetailPage />);
    expect(await screen.findByText("Projeto não encontrado")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Voltar para os projetos/ })).toHaveAttribute("href", "/projetos/");
  });
});

describe("Aplicação", () => {
  it("visão mostra status, commit, endereço e deploy agora leva à aba de deploys", async () => {
    routes["POST /api/apps/5/deploys"] = () =>
      json({ deploy: { id: 4, appId: 5, status: "queued" } }, 202);
    nav.search = "id=5";
    renderPage(<AppDetailPage />);
    expect(await screen.findByRole("heading", { level: 1, name: "loja-web" })).toBeInTheDocument();
    expect(screen.getAllByText("No ar").length).toBeGreaterThan(0);
    expect(screen.getByText("0123456")).toBeInTheDocument();
    // Endereço local é do servidor: texto, não link (no navegador, 127.0.0.1 é a máquina de quem vê).
    expect(screen.getByText("http://127.0.0.1:20001")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /127\.0\.0\.1/ })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /loja\.exemplo\.com/ })).toHaveAttribute("href", "https://loja.exemplo.com");

    fireEvent.click(screen.getByRole("button", { name: "Deploy agora" }));
    await waitFor(() => expect(screen.getByRole("tab", { name: "Deploys" })).toHaveAttribute("aria-selected", "true"));
    expect(await screen.findByRole("heading", { name: "Deploys" })).toBeInTheDocument();
  });

  it("visão mostra o último deploy que falhou", async () => {
    routes["GET /api/apps/5/deploys"] = () =>
      json({ deploys: [{ id: 4, appId: 5, status: "failed", reason: "verificação de saúde expirou" }] });
    nav.search = "id=5";
    renderPage(<AppDetailPage />);
    expect(await screen.findByText("O deploy #4 falhou.")).toBeInTheDocument();
    expect(screen.getByText(/verificação de saúde expirou/)).toBeInTheDocument();
    expect(screen.getByText(/A versão anterior continua no ar/)).toBeInTheDocument();
  });

  it("remover aplicação com domínios explica o motivo", async () => {
    routes["DELETE /api/apps/5"] = () =>
      json({ error: { code: "APP_HAS_DOMAINS", message: "Remova os domínios da aplicação antes de apagá-la." } }, 409);
    nav.search = "id=5&aba=configuracoes";
    renderPage(<AppDetailPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Remover aplicação" }));
    const dialog = screen.getByRole("dialog", { name: "Remover aplicação?" });
    const confirm = within(dialog).getByRole("button", { name: "Remover" });
    expect(confirm).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/Digite loja-web/), { target: { value: "loja-web" } });
    expect(confirm).toBeEnabled();
    fireEvent.click(confirm);
    expect(await within(dialog).findByText(/ainda tem domínios/)).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: /Domínios/ })).toHaveAttribute("href", "/dominios/");
    expect(nav.push).not.toHaveBeenCalled();
  });

  it("aplicação inexistente mostra não encontrada", async () => {
    nav.search = "id=abc";
    renderPage(<AppDetailPage />);
    expect(await screen.findByText("Aplicação não encontrada")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Voltar para os projetos/ })).toHaveAttribute("href", "/projetos/");
  });
});
