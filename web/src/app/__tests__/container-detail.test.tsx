import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import DetalhePage from "../(painel)/containers/detalhe/page";

const nav = vi.hoisted(() => ({ search: "", push: vi.fn() }));

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(nav.search),
  useRouter: () => ({ push: nav.push, replace: nav.push }),
}));

class FakeWS {
  static instances: FakeWS[] = [];
  readyState = 0;
  sent: { type: string; topic: string }[] = [];
  onopen: (() => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeWS.instances.push(this);
  }
  send(s: string) {
    this.sent.push(JSON.parse(s));
  }
  close() {
    this.readyState = 3;
    this.onclose?.({ code: 1000 });
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  msg(topic: string, data: unknown) {
    this.onmessage?.({ data: JSON.stringify({ type: "event", topic, data }) });
  }
}

const ID = "b".repeat(12);

const details = {
  id: ID,
  name: "loja-web",
  image: "nginx:1.27",
  state: "running",
  status: "",
  health: "healthy",
  createdAt: "2026-11-06T12:00:00Z",
  startedAt: "2026-11-06T12:00:05Z",
  project: "loja",
  service: "web",
  managed: false,
  ports: [{ ip: "0.0.0.0", publicPort: 8080, privatePort: 80, proto: "tcp" }],
  command: "nginx -g 'daemon off;'",
  restartCount: 2,
  envNames: ["DB_PASSWORD", "PORT"],
  networks: ["loja_default"],
  mounts: [{ type: "volume", source: "loja_data", destination: "/data", readWrite: true }],
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

let detailRoute: () => Response;

beforeEach(() => {
  FakeWS.instances = [];
  nav.search = `id=${ID}`;
  nav.push.mockReset();
  detailRoute = () => json(details);
  vi.stubGlobal("WebSocket", FakeWS);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const path = url.split("?")[0];
      if (path === `/api/containers/${ID}`) return detailRoute();
      if (path === `/api/containers/${ID}/logs`) return json({ lines: [] });
      return new Response(null, { status: 204 });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <DetalhePage />
    </QueryClientProvider>,
  );
}

const subs = (ws: FakeWS, type: string) => ws.sent.filter((m) => m.type === type).map((m) => m.topic);

describe("Detalhe do container", () => {
  it("detalhe mostra nomes das variáveis e nunca valores", async () => {
    const { container } = renderPage();
    expect(await screen.findByRole("heading", { level: 1, name: "loja-web" })).toBeInTheDocument();

    // Cabeçalho: selo, estado e ações.
    expect(screen.getByText("externo")).toBeInTheDocument();
    expect(screen.getAllByText("running")[0]).toHaveAttribute("data-variant", "success");
    expect(screen.getByRole("button", { name: "Parar" })).toBeInTheDocument();

    const vars = screen.getByRole("table", { name: "Variáveis de ambiente" });
    expect(within(vars).getByText("DB_PASSWORD")).toBeInTheDocument();
    expect(within(vars).getByText("PORT")).toBeInTheDocument();
    expect(within(vars).getAllByText("••••••")).toHaveLength(2);
    expect(container.textContent).not.toMatch(/DB_PASSWORD\s*=/);

    expect(screen.getByText("nginx -g 'daemon off;'")).toBeInTheDocument();
    expect(screen.getByText("0.0.0.0:8080→80/tcp")).toBeInTheDocument();
    expect(screen.getByText("loja_default")).toBeInTheDocument();
    expect(screen.getByText("loja_data")).toBeInTheDocument();
    expect(screen.getByText("/data")).toBeInTheDocument();
  });

  it("aba Stats só assina o tópico enquanto está aberta", async () => {
    renderPage();
    await screen.findByRole("heading", { level: 1, name: "loja-web" });
    const ws = FakeWS.instances[0];
    act(() => ws.open());
    const topic = `container-stats:${ID}`;
    expect(subs(ws, "subscribe")).not.toContain(topic);

    fireEvent.click(screen.getByRole("tab", { name: "Stats" }));
    expect(subs(ws, "subscribe")).toContain(topic);

    act(() => ws.msg(topic, { cpuPercent: 12.5, memUsedBytes: 64 * 1024 ** 2, memLimitBytes: 512 * 1024 ** 2 }));
    expect(screen.getByText("12,5%")).toBeInTheDocument();
    expect(screen.getByText("64,0 MiB")).toBeInTheDocument();

    act(() => ws.msg(topic, { running: false }));
    expect(screen.getByText("Container parado")).toBeInTheDocument();

    // Amostra nova limpa o aviso; a amostra vazia que o Docker manda com o container parado conta como parado.
    act(() => ws.msg(topic, { cpuPercent: 1, memUsedBytes: 1024, memLimitBytes: 2048 }));
    expect(screen.queryByText("Container parado")).not.toBeInTheDocument();
    act(() => ws.msg(topic, { cpuPercent: 0, memUsedBytes: 0, memLimitBytes: 0 }));
    expect(screen.getByText("Container parado")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Detalhes" }));
    expect(subs(ws, "unsubscribe")).toContain(topic);
  });

  it("?aba=logs abre a aba Logs e trocar de aba atualiza a URL", async () => {
    nav.search = `id=${ID}&aba=logs`;
    renderPage();
    expect(await screen.findByRole("tab", { name: "Logs" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("region", { name: "Saída de loja-web" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Stats" }));
    expect(window.location.search).toBe(`?id=${ID}&aba=stats`);
  });

  it("404 mostra Container não encontrado com link para a lista", async () => {
    detailRoute = () => json({ error: { code: "CONTAINER_NOT_FOUND", message: "não existe" } }, 404);
    renderPage();
    expect(await screen.findByText("Container não encontrado")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /lista de containers/i })).toHaveAttribute("href", "/containers/");
  });

  it("ID inválido mostra Container não encontrado sem chamar a API", async () => {
    nav.search = "id=NAO-HEX";
    renderPage();
    expect(await screen.findByText("Container não encontrado")).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("503 mostra Docker indisponível com Tentar novamente", async () => {
    detailRoute = () => json({ error: { code: "DOCKER_UNAVAILABLE", message: "fora" } }, 503);
    renderPage();
    expect(await screen.findByText("Docker indisponível")).toBeInTheDocument();
    detailRoute = () => json(details);
    fireEvent.click(screen.getByRole("button", { name: "Tentar novamente" }));
    expect(await screen.findByRole("heading", { level: 1, name: "loja-web" })).toBeInTheDocument();
  });
});
