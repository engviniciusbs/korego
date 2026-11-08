import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DeployLogEntry } from "@/lib/deploys";
import { DeployLog } from "../deploy-log";

class FakeWS {
  static instances: FakeWS[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  sent: string[] = [];
  constructor(public url: string) {
    FakeWS.instances.push(this);
  }
  send(m: string) {
    this.sent.push(m);
  }
  close() {
    this.readyState = 3;
    this.onclose?.({ code: 1000 });
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
}

const ID = 7;
const TOPIC = `deploy-logs:${ID}`;
const E = (n: number): DeployLogEntry => ({ n, text: `linha-${n}` });

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function push(topic: string, data: unknown) {
  const ws = FakeWS.instances.at(-1)!;
  act(() => {
    ws.onmessage?.({ data: JSON.stringify({ type: "event", topic, data }) });
  });
}

/** Buscas do log pendentes, na ordem: `from` e o resolvedor. */
let logCalls: { from: number; resolve: (entries: DeployLogEntry[], done?: boolean) => void }[];

let deploy: Record<string, unknown>;
const baseDeploy = {
  id: ID,
  appId: 5,
  trigger: "manual",
  status: "building",
  reason: "",
  commitSha: "",
  commitMessage: "",
  image: "",
  hostPort: 0,
  rollbackOf: null,
  createdAt: "2026-11-08T12:00:00Z",
  startedAt: "2026-11-08T12:00:01Z",
  finishedAt: null,
};

beforeEach(() => {
  FakeWS.instances = [];
  logCalls = [];
  deploy = { ...baseDeploy };
  vi.stubGlobal("WebSocket", FakeWS);
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      const m = /^\/api\/deploys\/7\/log\?from=(\d+)$/.exec(url);
      if (m) {
        return new Promise<Response>((resolve) => {
          logCalls.push({
            from: Number(m[1]),
            resolve: (entries, done = false) => resolve(json({ entries, done })),
          });
        });
      }
      if (url === "/api/deploys/7") return Promise.resolve(json({ deploy }));
      return Promise.resolve(json({ error: { code: "NOT_FOUND", message: "não" } }, 404));
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderLog() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <DeployLog id={ID} />
    </QueryClientProvider>,
  );
}

const shown = () =>
  within(screen.getByRole("region", { name: "Saída do deploy #7" }))
    .queryAllByTestId("log-text")
    .map((el) => el.textContent);

const range = (a: number, b: number) => Array.from({ length: b - a + 1 }, (_, i) => `linha-${a + i}`);

describe("DeployLog", () => {
  it("DeployLog junta histórico e ao vivo sem duplicar e rebusca na lacuna", async () => {
    renderLog();
    act(() => FakeWS.instances[0].open());
    // Assina antes de buscar: o que chega antes do histórico fica guardado.
    expect(FakeWS.instances[0].sent.some((s) => s.includes(TOPIC))).toBe(true);
    await waitFor(() => expect(logCalls).toHaveLength(1));
    expect(logCalls[0].from).toBe(0);
    push(TOPIC, E(3));
    push(TOPIC, E(4));
    expect(shown()).toEqual([]);

    await act(async () => logCalls[0].resolve([E(1), E(2), E(3)]));
    await waitFor(() => expect(shown()).toEqual(range(1, 4)));

    // Repetida é descartada; a seguinte entra direto.
    push(TOPIC, E(4));
    push(TOPIC, E(5));
    expect(shown()).toEqual(range(1, 5));

    // Lacuna (6 e 7 perdidas): busca de novo a partir da última contígua.
    push(TOPIC, E(8));
    await waitFor(() => expect(logCalls).toHaveLength(2));
    expect(logCalls[1].from).toBe(5);
    push(TOPIC, E(9));
    await act(async () => logCalls[1].resolve([E(6), E(7), E(8)]));
    await waitFor(() => expect(shown()).toEqual(range(1, 9)));

    push(TOPIC, { done: true });
    expect(await screen.findByText("Log completo")).toBeInTheDocument();
  });

  it("pagina o histórico enquanto vier cheio", async () => {
    renderLog();
    act(() => FakeWS.instances[0].open());
    await waitFor(() => expect(logCalls).toHaveLength(1));
    const page = Array.from({ length: 5000 }, (_, i) => E(i + 1));
    await act(async () => logCalls[0].resolve(page));
    await waitFor(() => expect(logCalls).toHaveLength(2));
    expect(logCalls[1].from).toBe(5000);
    await act(async () => logCalls[1].resolve([E(5001)], true));
    await waitFor(() => expect(shown()).toHaveLength(5001));
    expect(screen.getByText("Log completo")).toBeInTheDocument();
  });

  it("mostra o motivo quando o deploy falha pelo tópico deploys", async () => {
    renderLog();
    act(() => FakeWS.instances[0].open());
    expect(await screen.findByText("Construindo")).toBeInTheDocument();
    // Evento de outro deploy é ignorado.
    push("deploys", { id: 99, appId: 5, status: "failed", reason: "outro" });
    deploy = { ...baseDeploy, status: "failed", reason: "build falhou: exit 1", finishedAt: "2026-11-08T12:00:31Z" };
    push("deploys", { id: ID, appId: 5, status: "failed", reason: "build falhou: exit 1" });
    expect(await screen.findByText("Falhou")).toBeInTheDocument();
    expect(screen.getByText("build falhou: exit 1")).toBeInTheDocument();
    expect(screen.queryByText("outro")).toBeNull();
  });
});
