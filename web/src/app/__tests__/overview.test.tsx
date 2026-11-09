import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import VisaoGeralPage from "../(painel)/page";

class FakeWS {
  static instances: FakeWS[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeWS.instances.push(this);
  }
  send() {}
  close() {
    this.readyState = 3;
    this.onclose?.({ code: 1000 });
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  msg(o: unknown) {
    this.onmessage?.({ data: JSON.stringify(o) });
  }
}

const GiB = 1024 ** 3;

const sample = (over: Record<string, unknown> = {}) => ({
  time: "2026-11-06T12:00:00Z",
  cpuPercent: 10,
  memUsedBytes: 2 * GiB,
  memTotalBytes: 8 * GiB,
  load1: 0.5,
  load5: 0.4,
  load15: 0.3,
  netRxBytesPerSec: 1024,
  netTxBytesPerSec: 2048,
  disks: [{ mount: "/", fstype: "ext4", usedBytes: 20 * GiB, totalBytes: 40 * GiB }],
  ...over,
});

const system = {
  hostname: "vps-1",
  os: "linux",
  platform: "ubuntu",
  platformVersion: "24.04",
  kernel: "6.8",
  arch: "amd64",
  cpus: 2,
  memoryTotalBytes: 8 * GiB,
  uptimeSeconds: 3600,
};

const hp = (t: number) => ({
  t,
  cpuPercent: 20,
  memUsedBytes: GiB,
  memTotalBytes: 8 * GiB,
  load1: 0.1,
  netRxBytesPerSec: 100,
  netTxBytesPerSec: 50,
  diskUsedBytes: GiB,
  diskTotalBytes: 2 * GiB,
});

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

type Routes = Record<string, () => Response>;

function mockApi(over: Routes = {}) {
  const routes: Routes = {
    "/api/system": () => json(system),
    "/api/metrics/current": () => json(sample()),
    "/api/metrics/history": () => json({ range: "1h", stepSeconds: 60, points: [] }),
    "/api/containers": () =>
      json({
        containers: [
          { id: "a".repeat(12), name: "web", state: "running" },
          { id: "b".repeat(12), name: "db", state: "exited" },
        ],
      }),
    "/api/pm2": () => json({ available: false, daemonRunning: false, processes: [] }),
    "/api/usage": () => json({ error: { code: "USAGE_NOT_READY", message: "coletando" } }, 503),
    ...over,
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const path = url.split("?")[0];
      const h = routes[path];
      return h ? h() : json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
    }),
  );
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}>
      <VisaoGeralPage />
    </QueryClientProvider>,
  );
  return { ...view, client };
}

beforeEach(() => {
  FakeWS.instances = [];
  vi.stubGlobal("WebSocket", FakeWS);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const owner = (key: string, label: string, cpuPercent: number, memBytes: number, kind = "service") => ({
  key,
  kind,
  label,
  cpuPercent,
  memBytes,
  procCount: 1,
});

/** Mesmo formato do tooltip do gráfico. */
const tooltipTime = (t: number) =>
  new Intl.DateTimeFormat("pt-BR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" }).format(
    new Date(t * 1000),
  );

/** Histórico de 1h com 3 pontos; devolve o t do último. */
function withHistory(atBody: () => Response) {
  const now = Math.floor(Date.now() / 1000);
  const last = now - 60;
  mockApi({
    "/api/metrics/history": () =>
      json({ range: "1h", stepSeconds: 60, points: [hp(now - 180), hp(now - 120), hp(last)] }),
    "/api/usage/at": atBody,
  });
  return last;
}

describe("Visão geral", () => {
  it("visão geral mostra os valores ao vivo do canal metrics", async () => {
    mockApi();
    renderPage();
    const cpu = await screen.findByRole("region", { name: "CPU" });
    expect(await within(cpu).findByText("10,0%")).toBeInTheDocument();

    const ws = FakeWS.instances[FakeWS.instances.length - 1];
    act(() => ws.open());
    act(() =>
      ws.msg({
        type: "event",
        topic: "metrics",
        data: sample({ cpuPercent: 42.5, load1: 1.25, netRxBytesPerSec: 3 * 1024 * 1024 }),
      }),
    );

    expect(within(cpu).getByText("42,5%")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Load" })).getByText("1,25")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Rede" })).getByText("3,0 MiB/s")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Memória" })).getByText("2,0 GiB")).toBeInTheDocument();
    expect(screen.getByText("/")).toBeInTheDocument();
    expect(screen.getByText("Containers: 1 rodando de 2")).toBeInTheDocument();
    // Fora do build o next/link remove a barra final; com trailingSlash: true ela volta.
    expect(screen.getByRole("link", { name: /Ver containers/ })).toHaveAttribute(
      "href",
      expect.stringMatching(/^\/containers\/?$/),
    );
  });

  it("bloco PM2 some quando available é false e avisa quando o daemon está parado", async () => {
    mockApi();
    const first = renderPage();
    // Só afirma a ausência depois que /api/pm2 respondeu e a resposta foi renderizada.
    await waitFor(() => expect(fetch).toHaveBeenCalledWith("/api/pm2", expect.anything()));
    await waitFor(() => expect(first.client.getQueryState(["pm2"])?.status).toBe("success"));
    await act(async () => {});
    expect(first.client.getQueryData(["pm2"])).toEqual({ available: false, daemonRunning: false, processes: [] });
    expect(screen.queryByRole("region", { name: "Processos PM2" })).toBeNull();
    first.unmount();

    mockApi({ "/api/pm2": () => json({ available: true, daemonRunning: false, processes: [] }) });
    renderPage();
    const block = await screen.findByRole("region", { name: "Processos PM2" });
    expect(within(block).getByText("O PM2 está instalado, mas o daemon não está ativo.")).toBeInTheDocument();
  });

  it("bloco PM2 lista os processos", async () => {
    mockApi({
      "/api/pm2": () =>
        json({
          available: true,
          daemonRunning: true,
          processes: [
            {
              name: "api",
              pmId: 0,
              status: "online",
              cpuPercent: 1.5,
              memoryBytes: 50 * 1024 * 1024,
              restarts: 3,
              cwd: "/srv/api",
              uptimeSeconds: 60,
            },
          ],
        }),
    });
    renderPage();
    const block = await screen.findByRole("region", { name: "Processos PM2" });
    const row = within(block).getByRole("row", { name: /api/ });
    expect(within(row).getByText("online")).toBeInTheDocument();
    expect(within(row).getByText("50,0 MiB")).toBeInTheDocument();
    expect(within(row).getByText("3")).toBeInTheDocument();
    expect(within(row).getByText("/srv/api")).toBeInTheDocument();
  });

  it("troca de período mantém os gráficos montados enquanto o novo chega", async () => {
    const now = Math.floor(Date.now() / 1000);
    let release24h: () => void = () => {};
    const gate = new Promise<void>((r) => {
      release24h = r;
    });
    mockApi();
    const base = vi.mocked(fetch).getMockImplementation()!;
    vi.mocked(fetch).mockImplementation(async (url, init) => {
      const u = String(url);
      if (u === "/api/metrics/history?range=1h") {
        return json({ range: "1h", stepSeconds: 60, points: [hp(now - 120), hp(now - 60)] });
      }
      if (u === "/api/metrics/history?range=24h") {
        await gate;
        return json({ range: "24h", stepSeconds: 60, points: [hp(now - 60)] });
      }
      return base(url as string, init);
    });
    renderPage();
    const cpuChart = await screen.findByRole("region", { name: "Uso de CPU" });
    await within(cpuChart).findByRole("img");

    fireEvent.click(screen.getByRole("tab", { name: "24h" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith("/api/metrics/history?range=24h", expect.anything()));
    // Enquanto o 24h não chega, o gráfico de 1h continua na tela, sem spinner por painel.
    expect(within(cpuChart).getByRole("img")).toBeInTheDocument();
    expect(screen.queryByText("Carregando histórico…")).toBeNull();
    expect(screen.getByText("Carregando o novo período…")).toBeInTheDocument();

    await act(async () => release24h());
    await waitFor(() => expect(screen.queryByText("Carregando o novo período…")).toBeNull());
    expect(within(cpuChart).getByRole("img")).toBeInTheDocument();
  });

  it("resumo de containers mostra Docker indisponível em 503", async () => {
    mockApi({
      "/api/containers": () =>
        json({ error: { code: "DOCKER_UNAVAILABLE", message: "Docker indisponível" } }, 503),
    });
    renderPage();
    const box = await screen.findByRole("region", { name: "Containers" });
    expect(await within(box).findByText("Docker indisponível")).toBeInTheDocument();
    expect(within(box).getByRole("button", { name: "Tentar novamente" })).toBeInTheDocument();
  });

  it("lembra de configurar o domínio do painel e mostra o proxy bloqueado", async () => {
    const panel = (domain: string) => () =>
      json({
        domain,
        domainId: 0,
        cert: { state: "pending", notAfter: "0001-01-01T00:00:00Z" },
        publicPort: true,
        listenAddr: "0.0.0.0:8000",
        requestHTTPS: false,
        requestHost: "203.0.113.10",
      });
    mockApi({
      "/api/settings/panel": panel(""),
      "/api/proxy": () => json({ state: "blocked", process: "apache2", guideUrl: "https://exemplo.com/guia" }),
    });
    const { unmount } = renderPage();
    const reminder = await screen.findByText("Configure o domínio do painel para usar HTTPS");
    expect(within(reminder.closest("[role=status]") as HTMLElement).getByRole("link", { name: /Configurar domínio/ }))
      .toHaveAttribute("href", "/configuracoes/painel/");
    expect(screen.getByText("Proxy inativo: as portas 80/443 estão em uso por apache2")).toBeInTheDocument();
    unmount();

    mockApi({ "/api/settings/panel": panel("painel.exemplo.com") });
    renderPage();
    await waitFor(() =>
      expect(vi.mocked(fetch).mock.calls.some((c) => c[0] === "/api/settings/panel")).toBe(true),
    );
    await screen.findByRole("region", { name: "Containers" });
    expect(screen.queryByText(/Configure o domínio do painel/)).toBeNull();
  });

  it("clicar no gráfico de CPU abre Quem estava usando ordenado por CPU", async () => {
    const last = withHistory(() =>
      json({
        t: last,
        stepSeconds: 60,
        metric: "cpu",
        owners: [
          owner("project:loja", "loja", 40, 512 * 1024 * 1024, "container"),
          owner("pm2:api", "api", 25.5, GiB, "pm2"),
          owner("service:nginx.service", "nginx.service", 9, 64 * 1024 * 1024),
          owner("session", "Sessões", 4, 32 * 1024 * 1024, "session"),
          owner("kernel", "Kernel", 2, 0, "kernel"),
          owner("other", "Outros", 1, 16 * 1024 * 1024, "other"),
        ],
      }),
    );
    renderPage();
    const panel = await screen.findByRole("region", { name: "Uso de CPU" });
    await within(panel).findByRole("img");
    const chart = within(panel).getByRole("group");
    expect(chart).toHaveClass("cursor-pointer");

    // Foco no gráfico seleciona o último ponto; o clique abre o diálogo nele.
    act(() => chart.focus());
    fireEvent.click(chart);

    const dialog = await screen.findByRole("dialog", { name: "Quem estava usando" });
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(`/api/usage/at?t=${last}&range=1h&metric=cpu`, expect.anything()),
    );
    expect(within(dialog).getByText(tooltipTime(last))).toBeInTheDocument();
    const items = await within(dialog).findAllByRole("listitem");
    expect(items).toHaveLength(5);
    expect(items.map((li) => within(li).getByRole("link").textContent)).toEqual([
      "loja",
      "api",
      "nginx.service",
      "Sessões",
      "Kernel",
    ]);
    expect(within(items[0]).getByText("40,0%")).toBeInTheDocument();
    expect(within(items[1]).getByText("1,0 GiB")).toBeInTheDocument();
    expect(within(items[0]).getByRole("link")).toHaveAttribute("href", expect.stringMatching(/^\/consumo\/?$/));

    // O foco começa no diálogo e volta ao gráfico ao fechar.
    const close = within(dialog).getByRole("button", { name: "Fechar" });
    expect(close).toHaveFocus();
    fireEvent.click(close);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(chart).toHaveFocus();
  });

  it("sem dados mostra a mensagem", async () => {
    const last = withHistory(() => json({ t: last, stepSeconds: 60, metric: "mem", owners: [] }));
    renderPage();
    const panel = await screen.findByRole("region", { name: "Memória usada" });
    await within(panel).findByRole("img");
    const chart = within(panel).getByRole("group");
    act(() => chart.focus());
    fireEvent.keyDown(chart, { key: "Enter" });

    const dialog = await screen.findByRole("dialog", { name: "Quem estava usando" });
    expect(await within(dialog).findByText("Sem dados para este momento.")).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledWith(`/api/usage/at?t=${last}&range=1h&metric=mem`, expect.anything());
    expect(within(dialog).queryByRole("listitem")).toBeNull();
  });

  it("painel Maiores consumidores mostra 5 e link para Consumo", async () => {
    let ready = false;
    const snapshot = {
      time: "2026-11-06T12:00:00Z",
      dockerOk: true,
      owners: [
        owner("service:a", "alfa", 3, GiB),
        owner("service:b", "beta", 30, GiB),
        owner("service:c", "gama", 12, GiB),
        owner("service:d", "delta", 1, GiB),
        owner("service:e", "épsilon", 50, GiB),
        owner("service:f", "zeta", 7, GiB),
        owner("service:g", "eta", 0.5, GiB),
      ].map((o) => ({ ...o, truncated: false, processes: [] })),
    };
    mockApi({
      "/api/usage": () =>
        ready ? json(snapshot) : json({ error: { code: "USAGE_NOT_READY", message: "coletando" } }, 503),
    });
    renderPage();
    const box = await screen.findByRole("region", { name: "Maiores consumidores" });
    // Antes do primeiro retrato: estado calmo de coleta, sem erro.
    expect(await within(box).findByText(/Coletando/)).toBeInTheDocument();
    expect(within(box).queryByRole("button", { name: "Tentar novamente" })).toBeNull();
    expect(within(box).getByRole("link", { name: /Ver consumo/ })).toHaveAttribute(
      "href",
      expect.stringMatching(/^\/consumo\/?$/),
    );

    // Primeira mensagem do tópico: entra a lista, ao vivo.
    ready = true;
    const ws = FakeWS.instances[FakeWS.instances.length - 1];
    act(() => ws.open());
    act(() => ws.msg({ type: "event", topic: "usage", data: snapshot }));
    const items = await within(box).findAllByRole("listitem");
    expect(items).toHaveLength(5);
    expect(items.map((li) => within(li).getByRole("link").textContent)).toEqual([
      "épsilon",
      "beta",
      "gama",
      "zeta",
      "alfa",
    ]);
    expect(within(items[0]).getByText("50,0%")).toBeInTheDocument();
    expect(within(items[0]).getByRole("link")).toHaveAttribute("href", expect.stringMatching(/^\/consumo\/?$/));

    // O tópico atualiza a ordem.
    act(() =>
      ws.msg({
        type: "event",
        topic: "usage",
        data: { ...snapshot, time: "2026-11-06T12:00:05Z", owners: [owner("service:g", "eta", 90, GiB), ...snapshot.owners] },
      }),
    );
    await waitFor(() => expect(within(box).getAllByRole("listitem")[0]).toHaveTextContent("eta"));
    expect(within(box).getByText("90,0%")).toBeInTheDocument();
  });
});
