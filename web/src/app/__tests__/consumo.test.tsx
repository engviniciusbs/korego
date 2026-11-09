import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import ConsumoPage from "../(painel)/consumo/page";

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

const MiB = 1024 ** 2;
const GiB = 1024 ** 3;

const proc = (pid: number, name: string, cmd: string, cpuPercent: number, memBytes: number) => ({
  pid,
  name,
  cmd,
  user: "app",
  cpuPercent,
  memBytes,
});

const owners = [
  {
    key: "service:nginx",
    kind: "service",
    label: "nginx",
    cpuPercent: 5,
    memBytes: 100 * MiB,
    procCount: 2,
    truncated: false,
    processes: [proc(80, "nginx", "nginx -g daemon off;", 5, 100 * MiB)],
  },
  {
    key: "project:loja",
    kind: "container",
    label: "loja",
    managed: true,
    cpuPercent: 30,
    memBytes: GiB,
    procCount: 3,
    truncated: false,
    processes: [
      proc(101, "node", "node server.js --token=••••", 20, 512 * MiB),
      proc(102, "postgres", "postgres -D /var/lib/postgresql/data", 10, 512 * MiB),
    ],
  },
  {
    key: "container:redis",
    kind: "container",
    label: "Avulso: redis",
    managed: false,
    cpuPercent: 1,
    memBytes: 2 * GiB,
    procCount: 1,
    truncated: false,
    processes: [proc(200, "redis-server", "redis-server *:6379", 1, 2 * GiB)],
  },
  {
    key: "pm2:api",
    kind: "pm2",
    label: "PM2: api",
    cpuPercent: 10,
    memBytes: 3 * GiB,
    procCount: 1,
    truncated: false,
    processes: [proc(300, "node", "node dist/main.js", 10, 3 * GiB)],
  },
];

const usage = (over: Record<string, unknown> = {}) => ({
  time: "2026-11-08T12:00:00Z",
  dockerOk: true,
  owners,
  ...over,
});

const metrics = {
  time: "2026-11-08T12:00:00Z",
  cpuPercent: 46,
  memUsedBytes: 6 * GiB,
  memTotalBytes: 8 * GiB,
  load1: 0,
  load5: 0,
  load15: 0,
  netRxBytesPerSec: 0,
  netTxBytesPerSec: 0,
  disks: [],
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

type Routes = Record<string, (url: string) => Response>;

function mockApi(over: Routes = {}) {
  const now = Math.floor(Date.now() / 1000);
  const routes: Routes = {
    "/api/usage": () => json(usage()),
    "/api/metrics/current": () => json(metrics),
    "/api/usage/history": () =>
      json({
        stepSeconds: 60,
        points: [
          { t: now - 120, cpuPercent: 25, memBytes: GiB },
          { t: now - 60, cpuPercent: 30, memBytes: GiB },
        ],
      }),
    ...over,
  };
  const fn = vi.fn(async (url: string) => {
    const h = routes[url.split("?")[0]];
    return h ? h(url) : json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}>
      <ConsumoPage />
    </QueryClientProvider>,
  );
  return { ...view, client };
}

/** Nomes dos donos na ordem da tabela (os botões de expandir). */
const ownerNames = () =>
  Array.from(document.querySelectorAll("tbody button[aria-expanded]")).map((b) => b.textContent?.trim());

beforeEach(() => {
  FakeWS.instances = [];
  vi.stubGlobal("WebSocket", FakeWS);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Consumo", () => {
  test("ordena por CPU e alterna para RAM", async () => {
    mockApi();
    renderPage();
    await screen.findByRole("button", { name: "loja" });

    expect(ownerNames()).toEqual(["loja", "PM2: api", "nginx", "Avulso: redis"]);
    expect(screen.getByRole("columnheader", { name: /CPU/ })).toHaveAttribute("aria-sort", "descending");

    fireEvent.click(screen.getByRole("button", { name: "RAM" }));
    expect(ownerNames()).toEqual(["PM2: api", "Avulso: redis", "loja", "nginx"]);
    expect(screen.getByRole("columnheader", { name: /RAM/ })).toHaveAttribute("aria-sort", "descending");
    expect(screen.getByRole("columnheader", { name: /CPU/ })).not.toHaveAttribute("aria-sort");

    // Barra de RAM relativa à memória total (8 GiB): 3 GiB = 37,5%.
    const row = screen.getByRole("button", { name: "PM2: api" }).closest("tr")!;
    expect(within(row).getByText("3,0 GiB")).toBeInTheDocument();
    await waitFor(() =>
      expect(row.querySelector("[data-bar='mem']")).toHaveStyle({ width: "37.5%" }),
    );
    expect(row.querySelector("[data-bar='cpu']")).toHaveStyle({ width: "10%" });
    expect(within(row).getByText("10,0%")).toBeInTheDocument();

    // Dica da coluna RAM.
    expect(screen.getByRole("button", { name: "RAM" })).toHaveAccessibleDescription(
      "Soma da memória residente dos processos; páginas compartilhadas podem ser contadas mais de uma vez.",
    );
  });

  test("filtra por tipo", async () => {
    mockApi();
    renderPage();
    await screen.findByRole("button", { name: "loja" });
    const filters = screen.getByRole("group", { name: "Filtrar por tipo" });

    fireEvent.click(within(filters).getByRole("button", { name: /PM2/ }));
    expect(within(filters).getByRole("button", { name: /PM2/ })).toHaveAttribute("aria-pressed", "true");
    expect(ownerNames()).toEqual(["PM2: api"]);

    fireEvent.click(within(filters).getByRole("button", { name: /Container/ }));
    expect(ownerNames()).toEqual(["loja", "Avulso: redis"]);
    const loja = screen.getByRole("button", { name: "loja" }).closest("tr")!;
    expect(within(loja).getByText("gerenciado")).toBeInTheDocument();
    const redis = screen.getByRole("button", { name: "Avulso: redis" }).closest("tr")!;
    expect(within(redis).getByText("externo")).toBeInTheDocument();

    fireEvent.click(within(filters).getByRole("button", { name: /Todos/ }));
    expect(ownerNames()).toHaveLength(4);
  });

  test("expandir carrega os processos com comando mascarado e o gráfico do dono", async () => {
    const fetchFn = mockApi();
    renderPage();
    const toggle = await screen.findByRole("button", { name: "loja" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const detail = document.getElementById(toggle.getAttribute("aria-controls")!)!;
    expect(detail).toBeInTheDocument();

    const procs = await within(detail).findByRole("table", { name: "Processos de loja" });
    expect(within(procs).getByText("node server.js --token=••••")).toBeInTheDocument();
    expect(within(procs).getByText("101")).toBeInTheDocument();
    expect(within(procs).getByText("postgres")).toBeInTheDocument();

    await waitFor(() =>
      expect(fetchFn).toHaveBeenCalledWith("/api/usage/history?owner=project%3Aloja&range=1h", expect.anything()),
    );
    expect(await within(detail).findByRole("img", { name: /CPU: 30,0%/ })).toBeInTheDocument();
    expect(within(detail).getByRole("img", { name: /RAM: 1,0 GiB/ })).toBeInTheDocument();

    const tabs = within(detail).getByRole("tablist", { name: "Período do histórico de loja" });
    fireEvent.click(within(tabs).getByRole("tab", { name: "24h" }));
    await waitFor(() =>
      expect(fetchFn).toHaveBeenCalledWith("/api/usage/history?owner=project%3Aloja&range=24h", expect.anything()),
    );

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(within(document.body).queryByRole("table", { name: "Processos de loja" })).toBeNull();
  });

  test("aviso de Docker indisponível", async () => {
    mockApi({ "/api/usage": () => json(usage({ dockerOk: false })) });
    renderPage();
    expect(
      await screen.findByText("Docker indisponível: os containers aparecem em Outros."),
    ).toBeInTheDocument();
  });

  test("sem aviso de Docker quando ele responde", async () => {
    mockApi();
    renderPage();
    await screen.findByRole("button", { name: "loja" });
    expect(screen.queryByText(/Docker indisponível/)).toBeNull();
  });

  test("mostra 'coletando' antes do primeiro retrato e entra com o tópico usage", async () => {
    mockApi({
      "/api/usage": () =>
        json({ error: { code: "USAGE_NOT_READY", message: "O consumo por dono ainda não está disponível." } }, 503),
    });
    renderPage();
    expect(await screen.findByText(/Coletando os primeiros dados/)).toBeInTheDocument();

    const ws = FakeWS.instances[FakeWS.instances.length - 1];
    act(() => ws.open());
    act(() =>
      ws.msg({
        type: "event",
        topic: "usage",
        data: {
          time: "2026-11-08T12:00:05Z",
          dockerOk: true,
          owners: [{ key: "kernel", kind: "kernel", label: "Kernel", cpuPercent: 2, memBytes: 0, procCount: 40 }],
        },
      }),
    );
    expect(await screen.findByRole("button", { name: "Kernel" })).toBeInTheDocument();
    expect(screen.queryByText(/Coletando os primeiros dados/)).toBeNull();
  });

  test("tópico usage atualiza os valores e mantém os processos já carregados", async () => {
    mockApi();
    renderPage();
    const toggle = await screen.findByRole("button", { name: "loja" });
    fireEvent.click(toggle);
    const detail = document.getElementById(toggle.getAttribute("aria-controls")!)!;
    await within(detail).findByText("node server.js --token=••••");

    const ws = FakeWS.instances[FakeWS.instances.length - 1];
    act(() => ws.open());
    act(() =>
      ws.msg({
        type: "event",
        topic: "usage",
        data: {
          time: "2026-11-08T12:00:05Z",
          dockerOk: true,
          // O tópico não traz processos nem `truncated`.
          owners: owners.map((o) => ({
            key: o.key,
            kind: o.kind,
            label: o.label,
            managed: o.managed,
            cpuPercent: o.key === "project:loja" ? 55.5 : o.cpuPercent,
            memBytes: o.memBytes,
            procCount: o.procCount,
          })),
        },
      }),
    );
    const row = screen.getByRole("button", { name: "loja" }).closest("tr")!;
    expect(await within(row).findByText("55,5%")).toBeInTheDocument();
    expect(within(detail).getByText("node server.js --token=••••")).toBeInTheDocument();
  });
  test("selo mostra Instantâneo sem WebSocket e Ao vivo com o tópico usage", async () => {
    mockApi();
    renderPage();
    await screen.findByRole("button", { name: "loja" });
    expect(screen.getByText("Instantâneo")).toBeInTheDocument();
    expect(screen.queryByText(/Ao vivo/)).toBeNull();

    const ws = FakeWS.instances[FakeWS.instances.length - 1];
    act(() => ws.open());
    act(() =>
      ws.msg({
        type: "event",
        topic: "usage",
        data: { time: "2026-11-08T12:00:05Z", dockerOk: true, owners: [] },
      }),
    );
    expect(await screen.findByText(/Ao vivo/)).toBeInTheDocument();

    act(() => ws.close());
    expect(await screen.findByText("Instantâneo")).toBeInTheDocument();
  });
  test("resposta REST atrasada não sobrescreve a mensagem mais nova do tópico", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => {
      release = r;
    });
    const fetchFn = mockApi();
    const sync = fetchFn.getMockImplementation()!;
    fetchFn.mockImplementation(async (url: string) => {
      if (url === "/api/usage") {
        await gate;
        return json(usage({ time: "2026-11-08T12:00:00Z" }));
      }
      return sync(url);
    });
    const { client } = renderPage();
    await waitFor(() => expect(fetchFn).toHaveBeenCalledWith("/api/usage", expect.anything()));

    const ws = FakeWS.instances[FakeWS.instances.length - 1];
    act(() => ws.open());
    act(() =>
      ws.msg({
        type: "event",
        topic: "usage",
        data: {
          time: "2026-11-08T12:00:05Z",
          dockerOk: true,
          owners: [
            { key: "project:loja", kind: "container", label: "loja", managed: true, cpuPercent: 55.5, memBytes: GiB, procCount: 3 },
          ],
        },
      }),
    );
    const row = (await screen.findByRole("button", { name: "loja" })).closest("tr")!;
    expect(within(row).getByText("55,5%")).toBeInTheDocument();

    await act(async () => {
      release();
      await gate;
    });
    // Deixa a resposta atrasada ser aplicada e renderizada.
    await waitFor(() => expect(fetchFn.mock.results.length).toBeGreaterThan(0));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20));
    });

    expect(within(screen.getByRole("button", { name: "loja" }).closest("tr")!).getByText("55,5%")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "PM2: api" })).toBeNull();
    expect(client.getQueryData<{ time: string }>(["usage", "current"])?.time).toBe("2026-11-08T12:00:05Z");
  });
});
