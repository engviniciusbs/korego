import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AppView } from "@/lib/apps";
import { StackPanel, UpdateModeForm } from "../stack-panel";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

type Handler = (init?: { method?: string; body?: string }) => Response;

let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

const app = {
  id: 20, projectId: 1, slug: "mikos", sourceType: "local", repo: "", image: "", buildType: "compose-local",
  branch: "", dockerfilePath: "", composePath: "", composeService: "", internalPort: 0, healthTimeoutS: 60,
  autoDeploy: false, hostPort: 0, currentDeploy: null, domains: [], url: "", staticBuildCmd: "", staticOutputDir: "",
  staticBaseDir: "", staticImage: "", staticSpa: false, staticImmutablePrefix: "", publishRoot: "",
  composeProject: "mikos", updateMode: "pull", updateScript: "", createdAt: "", updatedAt: "",
} as AppView;

const container = (id: string, name: string, project: string, state = "running") => ({
  id, name, image: "img", state, status: "Up", health: "", createdAt: "", project, service: name.split("-")[1] ?? "",
  managed: false, ports: [],
});

beforeEach(() => {
  routes = {
    "GET /api/apps/20/stack": () =>
      json({ project: "mikos", dir: "/srv/mikos", files: ["/srv/mikos/docker-compose.yml"], envPath: "/srv/mikos/.env" }),
    "GET /api/containers": () =>
      json({ containers: [container("c1", "mikos-web-1", "mikos"), container("c2", "outra-web-1", "outra")] }),
    "POST /api/apps/20/stack/stop": () => new Response(null, { status: 204 }),
    "POST /api/apps/20/stack/restart": () => new Response(null, { status: 204 }),
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

function renderUi(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

const posts = (url: string) => fetchMock.mock.calls.filter(([u, i]) => u === url && i?.method === "POST");

describe("StackPanel", () => {
  it("mostra pasta, arquivos e só os containers da stack", async () => {
    renderUi(<StackPanel app={app} />);
    expect(await screen.findByText("/srv/mikos")).toBeInTheDocument();
    expect(screen.getByText("/srv/mikos/docker-compose.yml")).toBeInTheDocument();
    const list = await screen.findByRole("list", { name: "Containers de mikos" });
    expect(within(list).getByText("web")).toBeInTheDocument();
    expect(screen.queryByText("outra-web-1")).toBeNull();
  });

  it("Reiniciar roda sem código", async () => {
    renderUi(<StackPanel app={app} />);
    fireEvent.click(screen.getByRole("button", { name: "Reiniciar" }));
    expect(await screen.findByText("Stack reiniciada.")).toBeInTheDocument();
    const [[, init]] = posts("/api/apps/20/stack/restart");
    expect(init.body).toBeUndefined();
  });

  it("Parar pede o código e o manda no corpo", async () => {
    renderUi(<StackPanel app={app} />);
    fireEvent.click(screen.getByRole("button", { name: "Parar" }));
    expect(await screen.findByText("Parar a stack mikos?")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Parar stack" }));
    expect(await screen.findByText("Informe o código de confirmação.")).toBeInTheDocument();
    expect(posts("/api/apps/20/stack/stop")).toEqual([]);

    fireEvent.change(screen.getByLabelText(/Código de confirmação/), { target: { value: "654321" } });
    fireEvent.click(screen.getByRole("button", { name: "Parar stack" }));
    expect(await screen.findByText("Stack parada.")).toBeInTheDocument();
    const [[, init]] = posts("/api/apps/20/stack/stop");
    expect(JSON.parse(init.body)).toEqual({ code: "654321" });
  });

  it("código recusado fica no diálogo", async () => {
    routes["POST /api/apps/20/stack/stop"] = () => json({ error: { code: "INVALID_CODE", message: "Código inválido." } }, 403);
    renderUi(<StackPanel app={app} />);
    fireEvent.click(screen.getByRole("button", { name: "Parar" }));
    fireEvent.change(await screen.findByLabelText(/Código de confirmação/), { target: { value: "654321" } });
    fireEvent.click(screen.getByRole("button", { name: "Parar stack" }));
    expect(await screen.findByText("Código inválido.")).toBeInTheDocument();
    expect(screen.getByText("Parar a stack mikos?")).toBeInTheDocument();
  });
});

describe("UpdateModeForm", () => {
  it("passar para o modo script pede o código e grava modo e caminho", async () => {
    routes["PUT /api/apps/20"] = (init) => json({ app: { ...app, ...JSON.parse(init?.body ?? "{}") } });
    renderUi(<UpdateModeForm app={app} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Atualizar com um script da stack" }));
    fireEvent.change(screen.getByLabelText(/Script de atualização/), { target: { value: "deploy.sh" } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar alterações" }));
    expect(await screen.findByText("Informe o código de confirmação.")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/Código de confirmação/), { target: { value: "111222" } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar alterações" }));
    expect(await screen.findByText("Alterações salvas.")).toBeInTheDocument();
    const put = fetchMock.mock.calls.find(([u, i]) => u === "/api/apps/20" && i?.method === "PUT");
    const body = JSON.parse(put![1].body);
    expect(body).toMatchObject({ sourceType: "local", buildType: "compose-local", updateMode: "script", updateScript: "deploy.sh", code: "111222" });
  });

  it("voltar para pull não pede código", async () => {
    routes["PUT /api/apps/20"] = () => json({ app });
    renderUi(<UpdateModeForm app={{ ...app, updateMode: "script", updateScript: "deploy.sh" }} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Atualizar com um script da stack" }));
    expect(screen.queryByLabelText(/Código de confirmação/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Salvar alterações" }));
    await waitFor(() => expect(screen.getByText("Alterações salvas.")).toBeInTheDocument());
    const put = fetchMock.mock.calls.find(([u, i]) => u === "/api/apps/20" && i?.method === "PUT");
    expect(JSON.parse(put![1].body)).toMatchObject({ updateMode: "pull", updateScript: "" });
  });
});
