import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LogLine } from "@/lib/logs";
import { LogViewer } from "../log-viewer";

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
}

const ID = "a".repeat(12);
const TOPIC = `container-logs:${ID}`;
const ts = (n: number) => String(1_762_430_400_000_000_000 + n * 1_000_000_000).padStart(19, "0");
const L = (n: number, text = `linha-${n}`, stream = "stdout"): LogLine => ({ ts: ts(n), stream, text });

/** Entrega uma mensagem do tópico de logs pelo WebSocket falso. */
function push(data: unknown) {
  const ws = FakeWS.instances.at(-1)!;
  act(() => {
    ws.onmessage?.({ data: JSON.stringify({ type: "event", topic: TOPIC, data }) });
  });
}

let resolveTail: (lines: LogLine[]) => void;

beforeEach(() => {
  FakeWS.instances = [];
  vi.stubGlobal("WebSocket", FakeWS);
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (url: string) =>
        new Promise<Response>((resolve) => {
          expect(url).toBe(`/api/containers/${ID}/logs?tail=500`);
          resolveTail = (lines) =>
            resolve(new Response(JSON.stringify({ lines }), { headers: { "Content-Type": "application/json" } }));
        }),
    ),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const output = () => screen.getByRole("region", { name: "Saída de web" });
const shown = () =>
  within(output())
    .queryAllByTestId("log-text")
    .map((el) => el.textContent);

/** Renderiza já com o tail carregado. */
async function renderLoaded(tail: LogLine[]) {
  render(<LogViewer containerId={ID} containerName="web" />);
  await act(async () => resolveTail(tail));
  await waitFor(() => expect(shown()).toHaveLength(tail.length));
}

/** scrollTop/scrollHeight controláveis no jsdom: grava cada atribuição a scrollTop. */
function fakeScroll(el: HTMLElement, { height = 1000, client = 100, top = 900 } = {}) {
  const sets: number[] = [];
  let current = top;
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => height });
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => client });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => current,
    set: (v: number) => {
      current = v;
      sets.push(v);
    },
  });
  return sets;
}

describe("LogViewer", () => {
  it("LogViewer junta tail e stream sem duplicar", async () => {
    render(<LogViewer containerId={ID} containerName="web" />);
    // Assina o tópico antes de buscar o tail; linhas que chegam antes ficam guardadas.
    act(() => FakeWS.instances[0].open());
    push({ lines: [L(3), L(4)] });
    expect(shown()).toEqual([]);

    await act(async () => resolveTail([L(1), L(2), L(3)]));
    await waitFor(() => expect(shown()).toEqual(["linha-1", "linha-2", "linha-3", "linha-4"]));

    // Ao vivo: repetidas são descartadas.
    push({ lines: [L(4), L(5)] });
    expect(shown()).toEqual(["linha-1", "linha-2", "linha-3", "linha-4", "linha-5"]);
  });

  it("LogViewer pausado não rola e conta as linhas novas", async () => {
    await renderLoaded([L(1), L(2)]);
    const sets = fakeScroll(output());

    // No fim da lista: linha nova rola até o fim.
    push({ lines: [L(3)] });
    expect(sets.at(-1)).toBe(1000);

    fireEvent.click(screen.getByRole("button", { name: "Pausar" }));
    sets.length = 0;
    push({ lines: [L(4), L(5)] });
    push({ lines: [L(6)] });
    expect(sets).toEqual([]);
    expect(shown()).toEqual(["linha-1", "linha-2", "linha-3"]);
    expect(screen.getByText("3 novas linhas")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Retomar" }));
    expect(shown()).toEqual(["linha-1", "linha-2", "linha-3", "linha-4", "linha-5", "linha-6"]);
    expect(sets.at(-1)).toBe(1000);
    expect(screen.queryByText(/novas linhas/)).not.toBeInTheDocument();
  });

  it("LogViewer não rola quando o usuário subiu a lista", async () => {
    await renderLoaded([L(1)]);
    const sets = fakeScroll(output(), { top: 200 });
    fireEvent.scroll(output());
    push({ lines: [L(2)] });
    expect(sets).toEqual([]);
  });

  it("LogViewer busca sem diferenciar maiúsculas", async () => {
    await renderLoaded([
      L(1, "Erro grave no banco"),
      L(2, "tudo ok"),
      L(3, "\u001b[31moutro ERRO\u001b[0m", "stderr"),
    ]);

    fireEvent.change(screen.getByLabelText("Buscar nos logs"), { target: { value: "erro" } });
    expect(shown()).toEqual(["Erro grave no banco", "outro ERRO"]);
    const marks = output().querySelectorAll("mark");
    expect([...marks].map((m) => m.textContent)).toEqual(["Erro", "ERRO"]);
    expect(screen.getByText("2 de 3 linhas")).toBeInTheDocument();

    // stderr tem marcação própria (cor + rótulo) e o ANSI foi removido.
    const errLine = screen.getByText("outro", { exact: false }).closest("[data-stream]");
    expect(errLine).toHaveAttribute("data-stream", "stderr");
  });

  it("LogViewer mostra aguardando reinício ao receber ended", async () => {
    await renderLoaded([L(1)]);
    push({ ended: true });
    expect(screen.getByText("Container parado; aguardando reinício…")).toBeInTheDocument();

    push({ lines: [L(2)] });
    expect(screen.queryByText("Container parado; aguardando reinício…")).not.toBeInTheDocument();
    expect(shown()).toEqual(["linha-1", "linha-2"]);
  });

  it("Baixar aponta para o download dos logs", async () => {
    await renderLoaded([]);
    expect(screen.getByRole("link", { name: "Baixar" })).toHaveAttribute(
      "href",
      `/api/containers/${ID}/logs/download`,
    );
  });
});
