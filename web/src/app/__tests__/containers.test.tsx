import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ContainersPage from "../(painel)/containers/page";

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

const ID = (ch: string) => ch.repeat(12);

const base = {
  image: "nginx:1.27",
  status: "Up 3 hours",
  health: "",
  createdAt: "2026-11-06T12:00:00Z",
  service: "",
  ports: [],
};

const list = [
  {
    ...base,
    id: ID("a"),
    name: "loja-web",
    state: "running",
    project: "loja",
    service: "web",
    managed: true,
    health: "healthy",
    ports: [{ ip: "0.0.0.0", publicPort: 8080, privatePort: 80, proto: "tcp" }],
  },
  { ...base, id: ID("b"), name: "loja-db", state: "exited", status: "Exited (0)", project: "loja", managed: true },
  {
    ...base,
    id: ID("c"),
    name: "redis",
    state: "running",
    project: "",
    managed: false,
    ports: [{ ip: "", publicPort: 0, privatePort: 6379, proto: "tcp" }],
  },
  { ...base, id: ID("d"), name: "api", state: "running", project: "blog", managed: false },
];

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

let containersRoute: () => Response;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  containersRoute = () => json({ containers: list });
  vi.stubGlobal("WebSocket", FakeWS);
  fetchMock = vi.fn(async (url: string, init?: { method?: string }) => {
    const path = url.split("?")[0];
    if (path === "/api/containers" && (!init?.method || init.method === "GET")) return containersRoute();
    return new Response(null, { status: 204 });
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
      <ContainersPage />
    </QueryClientProvider>,
  );
}

const row = (name: string) => screen.getByRole("link", { name }).closest("tr") as HTMLElement;

describe("Containers", () => {
  it("agrupa por projeto e mostra Avulsos", async () => {
    renderPage();
    await screen.findByRole("link", { name: "loja-web" });

    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings).toEqual([
      expect.stringMatching(/^blog/),
      expect.stringMatching(/^loja/),
      expect.stringMatching(/^Avulsos/),
    ]);

    const loja = screen.getByRole("region", { name: /^loja/ });
    expect(within(loja).getByText("2 containers")).toBeInTheDocument();
    expect(within(loja).getAllByRole("link").map((a) => a.textContent)).toEqual(["loja-db", "loja-web"]);
    const avulsos = screen.getByRole("region", { name: /^Avulsos/ });
    expect(within(avulsos).getByRole("link", { name: "redis" })).toBeInTheDocument();

    // Link para o detalhe, selo, estado, health e portas.
    expect(screen.getByRole("link", { name: "loja-web" })).toHaveAttribute(
      "href",
      `/containers/detalhe/?id=${ID("a")}`,
    );
    const web = row("loja-web");
    expect(within(web).getByText("gerenciado")).toBeInTheDocument();
    expect(within(web).getByText("running")).toHaveAttribute("data-variant", "success");
    expect(within(web).getByText("healthy")).toHaveAttribute("data-variant", "success");
    expect(within(web).getByText("0.0.0.0:8080→80/tcp")).toBeInTheDocument();
    expect(within(row("redis")).getByText("externo")).toBeInTheDocument();
    expect(within(row("redis")).getByText("6379/tcp")).toBeInTheDocument();

    // Filtro por nome: sem diferenciar maiúsculas e escondendo grupos vazios.
    fireEvent.change(screen.getByLabelText("Filtrar por nome"), { target: { value: "REDIS" } });
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
      expect.stringMatching(/^Avulsos/),
    ]);
    expect(screen.queryByRole("link", { name: "loja-web" })).not.toBeInTheDocument();
  });

  it("externo mostra o aviso no diálogo; gerenciado não", async () => {
    renderPage();
    await screen.findByRole("link", { name: "redis" });

    fireEvent.click(within(row("redis")).getByRole("button", { name: "Parar" }));
    let dialog = screen.getByRole("dialog", { name: /Parar/ });
    expect(within(dialog).getByText("Este container não foi criado pelo KoreGO")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Parar" })).toHaveAttribute("data-variant", "danger");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    fireEvent.click(within(row("loja-web")).getByRole("button", { name: "Reiniciar" }));
    dialog = screen.getByRole("dialog", { name: /Reiniciar/ });
    expect(within(dialog).queryByText("Este container não foi criado pelo KoreGO")).not.toBeInTheDocument();

    // Confirmar chama a API e fecha o diálogo.
    fireEvent.click(within(dialog).getByRole("button", { name: "Reiniciar" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith(`/api/containers/${ID("a")}/restart`, expect.objectContaining({ method: "POST" }));
  });

  it("erro da API aparece no diálogo pela mensagem do code", async () => {
    fetchMock.mockImplementation(async (url: string, init?: { method?: string }) => {
      if (init?.method === "DELETE") {
        return json({ error: { code: "CONTAINER_RUNNING", message: "container running" } }, 409);
      }
      return containersRoute();
    });
    renderPage();
    await screen.findByRole("link", { name: "loja-db" });

    fireEvent.click(within(row("loja-db")).getByRole("button", { name: "Remover" }));
    const dialog = screen.getByRole("dialog", { name: /Remover/ });
    fireEvent.click(within(dialog).getByRole("button", { name: "Remover" }));
    expect(await within(dialog).findByText("Pare o container para removê-lo.")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("Remover desabilitado com o container rodando", async () => {
    renderPage();
    await screen.findByRole("link", { name: "loja-web" });

    const running = within(row("loja-web"));
    expect(running.getByRole("button", { name: "Remover" })).toBeDisabled();
    expect(running.getByRole("button", { name: "Remover" })).toHaveAccessibleDescription(
      "Pare o container para removê-lo",
    );
    expect(running.queryByRole("button", { name: "Iniciar" })).not.toBeInTheDocument();

    const stopped = within(row("loja-db"));
    expect(stopped.getByRole("button", { name: "Remover" })).toBeEnabled();
    expect(stopped.getByRole("button", { name: "Iniciar" })).toBeEnabled();
    expect(stopped.queryByRole("button", { name: "Parar" })).not.toBeInTheDocument();
  });

  it("503 mostra Docker indisponível e Tentar novamente refaz a busca", async () => {
    containersRoute = () => json({ error: { code: "DOCKER_UNAVAILABLE", message: "docker fora" } }, 503);
    renderPage();

    expect(await screen.findByText("Docker indisponível")).toBeInTheDocument();
    const calls = () => fetchMock.mock.calls.filter(([u]) => u === "/api/containers").length;
    expect(calls()).toBe(1);

    containersRoute = () => json({ containers: list });
    fireEvent.click(screen.getByRole("button", { name: "Tentar novamente" }));
    expect(await screen.findByRole("link", { name: "loja-web" })).toBeInTheDocument();
    expect(calls()).toBe(2);
  });
});
