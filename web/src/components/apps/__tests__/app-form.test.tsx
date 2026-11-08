import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppForm } from "../app-form";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

type Handler = (init?: { method?: string; body?: string }) => Response;

let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

const created = {
  id: 7,
  projectId: 1,
  slug: "site",
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
  hostPort: 0,
  currentDeploy: null,
  domains: [],
  url: "",
  createdAt: "2026-11-08T12:00:00Z",
  updatedAt: "2026-11-08T12:00:00Z",
};

beforeEach(() => {
  routes = {
    "GET /api/github": () =>
      json({
        connected: true,
        app: { slug: "korego", owner: "exemplo", htmlUrl: "https://github.com/apps/korego", installUrl: "" },
        installations: [{ id: 1, account: "exemplo" }],
        panelReady: true,
      }),
    "GET /api/github/repos": () =>
      json({
        repos: [
          { fullName: "exemplo/loja", defaultBranch: "develop", installationId: 1 },
          { fullName: "exemplo/blog", defaultBranch: "main", installationId: 1 },
        ],
      }),
    "POST /api/apps": () => json({ app: created }, 201),
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

function renderForm(onSaved = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <AppForm projectId={1} onSaved={onSaved} />
    </QueryClientProvider>,
  );
  return onSaved;
}

const posts = () => fetchMock.mock.calls.filter(([u, i]) => u === "/api/apps" && i?.method === "POST");

describe("AppForm", () => {
  it("formulário mostra campos por origem e tipo de build", async () => {
    renderForm();

    // GitHub: seletor de repositório, branch e deploy automático.
    const loja = await screen.findByRole("button", { name: /exemplo\/loja/ });
    expect(screen.getByLabelText(/^Branch/)).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Deploy automático a cada push" })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /^Imagem/ })).not.toBeInTheDocument();

    // Escolher o repositório preenche o branch padrão quando o branch está vazio.
    fireEvent.change(screen.getByLabelText(/^Branch/), { target: { value: "" } });
    fireEvent.click(loja);
    expect(loja).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByLabelText(/^Branch/)).toHaveValue("develop");

    // URL Git pública + Dockerfile: URL, branch e caminho do Dockerfile; sem deploy automático.
    fireEvent.click(screen.getByRole("radio", { name: /URL Git/ }));
    expect(screen.getByLabelText(/^URL do repositório/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Caminho do Dockerfile/)).toHaveValue("Dockerfile");
    expect(screen.queryByLabelText(/^Caminho do Compose/)).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: "Deploy automático a cada push" })).not.toBeInTheDocument();

    // Compose: caminho do arquivo e serviço de entrada.
    fireEvent.change(screen.getByLabelText(/^Tipo de build/), { target: { value: "compose" } });
    expect(screen.getByLabelText(/^Caminho do Compose/)).toHaveValue("docker-compose.yml");
    expect(screen.getByLabelText(/^Serviço de entrada/)).toBeInTheDocument();

    // Imagem: só a imagem; o tipo de build fica fixo.
    fireEvent.click(screen.getByRole("radio", { name: /Imagem/ }));
    expect(screen.getByRole("textbox", { name: /^Imagem/ })).toBeInTheDocument();
    expect(screen.queryByLabelText(/^Branch/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/^Tipo de build/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/^Caminho do Compose/)).not.toBeInTheDocument();
    expect(screen.getByLabelText(/^Porta interna/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Verificação de saúde/)).toHaveValue(60);
  });

  it("GitHub sem App conectado mostra o link para Configurações → GitHub", async () => {
    routes["GET /api/github"] = () =>
      json({ connected: false, app: null, installations: [], panelReady: true });
    renderForm();
    const link = await screen.findByRole("link", { name: /Configurações → GitHub/ });
    expect(link).toHaveAttribute("href", "/configuracoes/github/");
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
  });

  it("slug inválido bloqueia o envio com a mensagem", async () => {
    const onSaved = renderForm();
    fireEvent.click(screen.getByRole("radio", { name: /Imagem/ }));
    const slug = screen.getByLabelText(/^Nome/);
    fireEvent.change(slug, { target: { value: "Meu App" } });
    // Validação ao digitar.
    expect(slug).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText(/minúsculas, números e hífen; começa com letra ou número/)).toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: /^Imagem/ }), { target: { value: "nginx:1-alpine" } });
    fireEvent.change(screen.getByLabelText(/^Porta interna/), { target: { value: "80" } });
    fireEvent.click(screen.getByRole("button", { name: "Criar aplicação" }));
    expect(await screen.findByText(/Nome inválido/)).toBeInTheDocument();
    expect(posts()).toHaveLength(0);

    fireEvent.change(slug, { target: { value: "site" } });
    expect(slug).not.toHaveAttribute("aria-invalid");
    fireEvent.click(screen.getByRole("button", { name: "Criar aplicação" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: 7 })));
    const body = JSON.parse(posts()[0][1].body);
    expect(body).toMatchObject({
      projectId: 1,
      slug: "site",
      sourceType: "image",
      buildType: "image",
      image: "nginx:1-alpine",
      internalPort: 80,
      healthTimeoutS: 60,
      autoDeploy: false,
    });
  });

  it("erro do servidor com campo aparece no campo", async () => {
    routes["POST /api/apps"] = () =>
      json({ error: { code: "APP_EXISTS", message: "Já existe uma aplicação com esse slug." } }, 409);
    renderForm();
    fireEvent.click(screen.getByRole("radio", { name: /Imagem/ }));
    fireEvent.change(screen.getByLabelText(/^Nome/), { target: { value: "site" } });
    fireEvent.change(screen.getByRole("textbox", { name: /^Imagem/ }), { target: { value: "nginx:1-alpine" } });
    fireEvent.change(screen.getByLabelText(/^Porta interna/), { target: { value: "80" } });
    fireEvent.click(screen.getByRole("button", { name: "Criar aplicação" }));
    expect(await screen.findByText("Já existe uma aplicação com esse nome.")).toBeInTheDocument();
    expect(screen.getByLabelText(/^Nome/)).toHaveAttribute("aria-invalid", "true");
  });

  it("Compose e Railpack não têm campo de Dockerfile nem o enviam", async () => {
    routes["POST /api/apps"] = () => json({ app: { ...created, sourceType: "git", buildType: "compose" } }, 201);
    renderForm();
    fireEvent.click(screen.getByRole("radio", { name: /URL Git/ }));
    expect(screen.getByLabelText(/^Caminho do Dockerfile/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/^Tipo de build/), { target: { value: "railpack" } });
    expect(screen.queryByLabelText(/^Caminho do Dockerfile/)).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/^Tipo de build/), { target: { value: "compose" } });
    expect(screen.queryByLabelText(/^Caminho do Dockerfile/)).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/^URL do repositório/), {
      target: { value: "https://github.com/exemplo/site.git" },
    });
    fireEvent.change(screen.getByLabelText(/^Serviço de entrada/), { target: { value: "web" } });
    fireEvent.change(screen.getByLabelText(/^Nome/), { target: { value: "site" } });
    fireEvent.change(screen.getByLabelText(/^Porta interna/), { target: { value: "80" } });
    fireEvent.click(screen.getByRole("button", { name: "Criar aplicação" }));
    await waitFor(() => expect(posts()).toHaveLength(1));
    const body = JSON.parse(posts()[0][1].body);
    expect(body).toMatchObject({ buildType: "compose", composePath: "docker-compose.yml", composeService: "web" });
    expect(body.dockerfilePath ?? "").toBe("");
  });

  it("compose mostra o aviso de recriação e de rollback", async () => {
    renderForm();
    const warning = "Compose recria os containers a cada deploy: alguns segundos fora do ar. Rollback não disponível.";
    expect(screen.queryByText(warning)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: /URL Git/ }));
    fireEvent.change(screen.getByLabelText(/^Tipo de build/), { target: { value: "compose" } });
    expect(screen.getByText(warning)).toBeInTheDocument();
  });
});
