import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AppDetailPage from "../(painel)/aplicacoes/detalhe/page";
import ProjectDetailPage from "../(painel)/projetos/detalhe/page";
import ProjetosPage from "../(painel)/projetos/page";
import SitePage from "../(painel)/projetos/site/page";

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

const vpsDomain = (over: Record<string, unknown>) => ({
  id: 6, hostname: "hoprs.online", targetType: "static", appId: null, appSlug: "", address: "",
  wwwRedirect: "none", forceHttps: true, isPanel: false,
  routes: [{ path: "/", targetType: "static", root: "/var/www/hoprs/current", maxBodyBytes: 0, noBuffering: false, spa: false }],
  tls: { mode: "custom", certId: 3, expiresSoon: false }, cloudflareOnly: true, headers: [], compression: true,
  cert: { state: "active", notAfter: "2041-11-10T13:43:00Z" },
  staticRoots: [{ path: "/", root: "/var/www/hoprs/current", resolved: "/var/www/hoprs/releases/20261115133041-9faa1c9", exists: true, modifiedAt: "2026-11-15T13:30:41Z" }],
  createdAt: "", updatedAt: "", ...over,
});

const vpsContainer = (over: Record<string, unknown>) => ({
  id: "c1", name: "mikos-caddy-1", image: "caddy:2", state: "running", status: "Up 5 days", health: "",
  createdAt: "", project: "mikos", service: "caddy", managed: false,
  ports: [{ ip: "127.0.0.1", privatePort: 80, publicPort: 8081, proto: "tcp" }], ...over,
});

describe("Sites na VPS", () => {
  beforeEach(() => {
    routes["GET /api/domains"] = () =>
      json({
        domains: [
          vpsDomain({}),
          vpsDomain({
            id: 4, hostname: "mikos.com.br", targetType: "address", staticRoots: [],
            routes: [{ path: "/", targetType: "address", address: "127.0.0.1:8081", maxBodyBytes: 0, noBuffering: false, spa: false }],
          }),
          vpsDomain({ id: 1, hostname: "painel.com", isPanel: true, staticRoots: [], routes: [] }),
        ],
      });
    routes["GET /api/containers"] = () =>
      json({
        containers: [
          vpsContainer({}),
          vpsContainer({ id: "c2", name: "mikos-api-1", service: "api", state: "exited", ports: [] }),
          vpsContainer({ id: "c3", name: "korego-caddy", project: "", managed: true }),
        ],
      });
  });

  it("Projetos mostra as stacks e os estáticos com selo, sem Remover", async () => {
    renderPage(<ProjetosPage />);
    const list = await screen.findByRole("list", { name: "Sites na VPS" });
    const mikos = within(list).getByRole("link", { name: "mikos" }).closest("li") as HTMLElement;
    expect(within(mikos).getByText("Docker compose")).toBeInTheDocument();
    expect(within(mikos).getByText("mikos.com.br")).toBeInTheDocument();
    expect(within(mikos).getByText("1 de 2 containers rodando")).toBeInTheDocument();
    expect(within(mikos).getByText("Parado")).toBeInTheDocument();
    expect(within(mikos).queryByRole("button", { name: /Remover/ })).toBeNull();
    expect(within(mikos).getByRole("button", { name: /Gerenciar pelo KoreGO/ })).toBeInTheDocument();

    const hoprs = within(list).getByRole("link", { name: "hoprs.online" });
    expect(hoprs).toHaveAttribute("href", "/projetos/site/?tipo=estatico&nome=hoprs.online");
    const card = hoprs.closest("li") as HTMLElement;
    expect(within(card).getByText("Estático")).toBeInTheDocument();
    expect(within(card).getByText("20261115133041-9faa1c9")).toBeInTheDocument();
    expect(within(card).getByText(/pasta \/var\/www\/hoprs\/current/)).toBeInTheDocument();

    expect(within(list).queryByText("painel.com")).toBeNull();
    expect(within(list).queryByText("korego-caddy")).toBeNull();
    expect(within(screen.getByRole("link", { name: "Loja" }).closest("li") as HTMLElement).getByText("KoreGO")).toBeInTheDocument();
  });

  it("stack adotada some de Na VPS e aparece no projeto", async () => {
    const adopted = app({
      id: 8, slug: "mikos", sourceType: "local", buildType: "compose-local", composeProject: "mikos",
      updateMode: "pull", updateScript: "", image: "", internalPort: 0, hostPort: 0, url: "",
      currentDeploy: null, domains: [],
    });
    routes["GET /api/apps"] = () => json({ apps: [app(), adopted] });
    renderPage(<ProjetosPage />);
    const list = await screen.findByRole("list", { name: "Sites na VPS" });
    expect(within(list).getByRole("link", { name: "hoprs.online" })).toBeInTheDocument();
    expect(within(list).queryByRole("link", { name: "mikos" })).toBeNull();
    expect(within(list).queryByText("mikos.com.br")).toBeNull();
    cleanup();

    nav.search = "id=1";
    renderPage(<ProjectDetailPage />);
    expect(await screen.findByRole("link", { name: "mikos" })).toHaveAttribute("href", "/aplicacoes/detalhe/?id=8&aba=visao");
  });

  it("pasta que sumiu aparece com aviso", async () => {
    routes["GET /api/domains"] = () =>
      json({ domains: [vpsDomain({ staticRoots: [{ path: "/", root: "/var/www/x", resolved: "", exists: false, modifiedAt: null }] })] });
    renderPage(<ProjetosPage />);
    expect(await screen.findByText("Pasta não encontrada: /var/www/x")).toBeInTheDocument();
    expect(screen.getByText("Sem pasta")).toBeInTheDocument();
  });

  it("sem Docker, os estáticos ainda aparecem com um aviso", async () => {
    routes["GET /api/containers"] = () => json({ error: { code: "DOCKER_UNAVAILABLE", message: "Docker parado" } }, 503);
    renderPage(<ProjetosPage />);
    expect(await screen.findByRole("link", { name: "hoprs.online" })).toBeInTheDocument();
    expect(screen.getByText("Não foi possível ler os containers.")).toBeInTheDocument();
  });

  it("a página do site estático mostra a pasta, a versão e o domínio", async () => {
    nav.search = "tipo=estatico&nome=hoprs.online";
    renderPage(<SitePage />);
    expect(await screen.findByRole("heading", { level: 1, name: "hoprs.online" })).toBeInTheDocument();
    expect(screen.getByText("Versão atual").nextElementSibling).toHaveTextContent("20261115133041-9faa1c9");
    expect(screen.getByRole("link", { name: /Editar em Domínios/ })).toHaveAttribute("href", "/dominios/");
    expect(screen.queryByRole("button", { name: /Remover|Apagar/ })).toBeNull();
  });

  it("a página da stack lista os containers com link para o detalhe", async () => {
    nav.search = "tipo=compose&nome=mikos";
    renderPage(<SitePage />);
    const list = await screen.findByRole("list", { name: "Containers de mikos" });
    expect(within(list).getByRole("link", { name: "caddy" })).toHaveAttribute("href", "/containers/detalhe/?id=c1");
    expect(within(list).getByText("exited")).toBeInTheDocument();
  });

  it("site inexistente mostra não encontrado", async () => {
    nav.search = "tipo=compose&nome=nada";
    renderPage(<SitePage />);
    expect(await screen.findByText("Site não encontrado")).toBeInTheDocument();
  });
});
