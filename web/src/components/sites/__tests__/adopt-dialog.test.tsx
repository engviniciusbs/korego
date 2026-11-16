import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { suggestSlug } from "@/lib/adopt";
import type { DetectedSite } from "@/lib/sites";
import { AdoptDialog } from "../adopt-dialog";

const nav = vi.hoisted(() => ({ push: vi.fn() }));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: nav.push, replace: nav.push }),
  useSearchParams: () => new URLSearchParams(""),
  usePathname: () => "/",
}));

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

type Handler = (init?: { method?: string; body?: string }) => Response;

let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

const appView = (over: Record<string, unknown>) => ({
  id: 12, projectId: 1, slug: "hoprs-online", sourceType: "github", repo: "exemplo/hoprs", image: "",
  buildType: "static", branch: "main", dockerfilePath: "", composePath: "", composeService: "",
  internalPort: 0, healthTimeoutS: 0, autoDeploy: false, hostPort: 0, currentDeploy: null, domains: ["hoprs.online"],
  url: "", staticBuildCmd: "", staticOutputDir: "dist", staticBaseDir: ".", staticImage: "node:22-alpine",
  staticSpa: true, staticImmutablePrefix: "/static/", publishRoot: "/var/www/hoprs", composeProject: "",
  updateMode: "", updateScript: "", createdAt: "", updatedAt: "", ...over,
});

const staticSite: DetectedSite = {
  kind: "estatico",
  name: "hoprs.online",
  containers: [],
  roots: [
    { path: "/", root: "/var/www/hoprs/current", resolved: "/var/www/hoprs/releases/20261115133041-9faa1c9", exists: true, modifiedAt: null },
  ],
  domains: [
    {
      id: 6, hostname: "hoprs.online", targetType: "static", appId: null, appSlug: "", address: "", wwwRedirect: "none",
      forceHttps: true, isPanel: false, cloudflareOnly: false, headers: [], compression: false,
      tls: { mode: "auto", certId: null, expiresSoon: false }, cert: { state: "active" } as never,
      routes: [
        { path: "/", targetType: "static", root: "/var/www/hoprs/current", maxBodyBytes: 0, noBuffering: false, spa: true, immutablePrefix: "/static/" },
      ],
      createdAt: "", updatedAt: "",
    },
  ],
};

const composeSite: DetectedSite = { kind: "compose", name: "mikos_stack", containers: [], roots: [], domains: [] };

beforeEach(() => {
  nav.push.mockReset();
  routes = {
    "GET /api/projects": () => json({ projects: [{ id: 1, name: "Sites", description: "", appCount: 0, createdAt: "" }] }),
    "GET /api/github": () =>
      json({ connected: true, app: { slug: "korego", owner: "exemplo", htmlUrl: "", installUrl: "" }, installations: [{ id: 1, account: "exemplo" }], panelReady: true }),
    "GET /api/github/repos": () => json({ repos: [{ fullName: "exemplo/hoprs", defaultBranch: "master", installationId: 1 }] }),
  };
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

function renderDialog(site: DetectedSite, onClose = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <AdoptDialog site={site} onClose={onClose} />
    </QueryClientProvider>,
  );
  return onClose;
}

const calls = (method: string, url: string) =>
  fetchMock.mock.calls.filter(([u, i]) => u === url && i?.method === method).map(([, i]) => JSON.parse(i.body ?? "{}"));

describe("suggestSlug", () => {
  it("troca pontos e caracteres inválidos por hífen", () => {
    expect(suggestSlug("Hoprs.Online")).toBe("hoprs-online");
    expect(suggestSlug("mikos_stack")).toBe("mikos-stack");
    expect(suggestSlug("-a..b-")).toBe("a-b");
    expect(suggestSlug("x".repeat(80))).toHaveLength(63);
  });
});

describe("AdoptDialog estático", () => {
  it("envia o corpo certo e mostra a caixa do Actions, que liga o deploy a cada push", async () => {
    routes["POST /api/adopt/static"] = () => json({ app: appView({}), domains: ["hoprs.online"] }, 201);
    routes["PUT /api/apps/12"] = (init) => json({ app: appView(JSON.parse(init?.body ?? "{}")) });
    const onClose = renderDialog(staticSite);

    expect(screen.getByText(/ficam intocadas; as do padrão entram na limpeza que guarda 5/)).toBeInTheDocument();
    expect(screen.getByLabelText(/Nome da aplicação/)).toHaveValue("hoprs-online");
    fireEvent.click(await screen.findByRole("button", { name: /exemplo\/hoprs/ }));
    fireEvent.click(screen.getByRole("button", { name: "Adotar site" }));

    expect(await screen.findByText("Site adotado")).toBeInTheDocument();
    expect(calls("POST", "/api/adopt/static")).toEqual([
      {
        projectId: 1,
        slug: "hoprs-online",
        current: "/var/www/hoprs/current",
        sourceType: "github",
        repo: "exemplo/hoprs",
        branch: "master",
        staticBuildCmd: "",
        staticOutputDir: "dist",
        staticBaseDir: ".",
        staticImage: "node:22-alpine",
        staticSpa: true,
        staticImmutablePrefix: "/static/",
      },
    ]);

    const box = screen.getByRole("checkbox", { name: "Já desliguei o workflow do Actions; publicar a cada push" });
    expect(box).not.toBeChecked();
    fireEvent.click(box);
    fireEvent.click(screen.getByRole("button", { name: "Ir para a aplicação" }));
    await waitFor(() => expect(nav.push).toHaveBeenCalledWith("/aplicacoes/detalhe/?id=12"));
    const [put] = calls("PUT", "/api/apps/12");
    expect(put.autoDeploy).toBe(true);
    expect(put.slug).toBe("hoprs-online");
    expect(onClose).toHaveBeenCalled();
  });

  it("prefixo imutável fora da regra do proxy não é enviado", async () => {
    renderDialog(staticSite);
    fireEvent.click(await screen.findByRole("button", { name: /exemplo\/hoprs/ }));
    fireEvent.change(screen.getByLabelText(/^Prefixo imutável/), { target: { value: "/_Next/" } });
    fireEvent.click(screen.getByRole("button", { name: "Adotar site" }));
    expect(await screen.findByText(/Prefixo inválido/)).toBeInTheDocument();
    expect(calls("POST", "/api/adopt/static")).toHaveLength(0);
  });

  it("sem marcar a caixa, vai para a aplicação sem mudar o deploy automático", async () => {
    routes["POST /api/adopt/static"] = () => json({ app: appView({}), domains: [] }, 201);
    renderDialog(staticSite);
    fireEvent.click(await screen.findByRole("button", { name: /exemplo\/hoprs/ }));
    fireEvent.click(screen.getByRole("button", { name: "Adotar site" }));
    fireEvent.click(await screen.findByRole("button", { name: "Ir para a aplicação" }));
    await waitFor(() => expect(nav.push).toHaveBeenCalledWith("/aplicacoes/detalhe/?id=12"));
    expect(calls("PUT", "/api/apps/12")).toEqual([]);
  });

  it("Novo projeto cria o projeto antes de adotar", async () => {
    routes["POST /api/projects"] = () => json({ project: { id: 7, name: "Novo", description: "", appCount: 0, createdAt: "" } }, 201);
    routes["POST /api/adopt/static"] = () => json({ app: appView({ projectId: 7 }), domains: [] }, 201);
    renderDialog(staticSite);
    const select = await screen.findByLabelText(/^Projeto/);
    fireEvent.change(select, { target: { value: "novo" } });
    fireEvent.change(screen.getByLabelText(/Nome do projeto/), { target: { value: "Novo" } });
    fireEvent.click(await screen.findByRole("button", { name: /exemplo\/hoprs/ }));
    fireEvent.click(screen.getByRole("button", { name: "Adotar site" }));
    expect(await screen.findByText("Site adotado")).toBeInTheDocument();
    expect(calls("POST", "/api/projects")).toEqual([{ name: "Novo", description: "" }]);
    expect(calls("POST", "/api/adopt/static")[0].projectId).toBe(7);
  });
});

describe("AdoptDialog compose", () => {
  it("modo pull adota sem código e abre a aplicação", async () => {
    routes["POST /api/adopt/compose"] = () =>
      json({ app: appView({ id: 20, slug: "mikos-stack", sourceType: "local", buildType: "compose-local" }) }, 201);
    renderDialog(composeSite);
    expect(await screen.findByLabelText(/Nome da aplicação/)).toHaveValue("mikos-stack");
    await screen.findByRole("option", { name: "Sites" });
    fireEvent.click(screen.getByRole("button", { name: "Adotar stack" }));
    await waitFor(() => expect(nav.push).toHaveBeenCalledWith("/aplicacoes/detalhe/?id=20"));
    expect(calls("POST", "/api/adopt/compose")).toEqual([
      { projectId: 1, slug: "mikos-stack", composeProject: "mikos_stack", updateMode: "pull" },
    ]);
  });

  it("modo script exige o código antes de enviar e o manda no corpo", async () => {
    routes["POST /api/adopt/compose"] = () =>
      json({ app: appView({ id: 20, slug: "mikos-stack", sourceType: "local", buildType: "compose-local" }) }, 201);
    renderDialog(composeSite);
    await screen.findByRole("option", { name: "Sites" });
    fireEvent.click(screen.getByRole("radio", { name: /Script da stack/ }));
    fireEvent.change(screen.getByLabelText(/Script de atualização/), { target: { value: "deploy.sh" } });
    fireEvent.click(screen.getByRole("button", { name: "Adotar stack" }));
    expect(await screen.findByText("Informe o código de confirmação.")).toBeInTheDocument();
    expect(calls("POST", "/api/adopt/compose")).toEqual([]);

    fireEvent.change(screen.getByLabelText(/Código de confirmação/), { target: { value: "123 456" } });
    fireEvent.click(screen.getByRole("button", { name: "Adotar stack" }));
    await waitFor(() => expect(nav.push).toHaveBeenCalledWith("/aplicacoes/detalhe/?id=20"));
    expect(calls("POST", "/api/adopt/compose")).toEqual([
      {
        projectId: 1,
        slug: "mikos-stack",
        composeProject: "mikos_stack",
        updateMode: "script",
        updateScript: "deploy.sh",
        code: "123456",
      },
    ]);
  });

  it("código recusado volta para o campo", async () => {
    routes["POST /api/adopt/compose"] = () => json({ error: { code: "INVALID_CODE", message: "Código inválido." } }, 403);
    renderDialog(composeSite);
    await screen.findByRole("option", { name: "Sites" });
    fireEvent.click(screen.getByRole("radio", { name: /Script da stack/ }));
    fireEvent.change(screen.getByLabelText(/Script de atualização/), { target: { value: "deploy.sh" } });
    fireEvent.change(screen.getByLabelText(/Código de confirmação/), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Adotar stack" }));
    expect(await screen.findByText("Código inválido.")).toBeInTheDocument();
    expect(screen.getByLabelText(/Código de confirmação/)).toHaveValue("");
    expect(nav.push).not.toHaveBeenCalled();
  });
});
