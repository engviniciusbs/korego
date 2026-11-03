import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LiveSocket } from "../ws";

class FakeWS {
  static instances: FakeWS[] = [];
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeWS.instances.push(this);
  }
  send(d: string) {
    this.sent.push(d);
  }
  close() {
    this.readyState = 3;
    this.onclose?.({ code: 1000 });
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  drop(code = 1006) {
    this.readyState = 3;
    this.onclose?.({ code });
  }
  msg(o: unknown) {
    this.onmessage?.({ data: JSON.stringify(o) });
  }
}

const make = () =>
  new LiveSocket({
    WebSocketImpl: FakeWS as unknown as typeof WebSocket,
    url: "ws://localhost/api/ws",
    random: () => 0.5, // jitter neutro
  });
const last = () => FakeWS.instances[FakeWS.instances.length - 1];

beforeEach(() => {
  vi.useFakeTimers();
  FakeWS.instances = [];
});
afterEach(() => vi.useRealTimers());

describe("LiveSocket", () => {
  it("reassina os tópicos após reconectar", () => {
    const s = make();
    s.subscribe("system", () => {});
    last().open();
    expect(last().sent).toEqual(['{"type":"subscribe","topic":"system"}']);
    last().drop();
    vi.advanceTimersByTime(1000);
    expect(FakeWS.instances).toHaveLength(2);
    last().open();
    expect(last().sent).toEqual(['{"type":"subscribe","topic":"system"}']);
  });

  it("aumenta o intervalo de reconexão até 30s", () => {
    const s = make();
    s.subscribe("system", () => {});
    const delays = [1000, 2000, 4000, 8000, 16000, 30000, 30000];
    delays.forEach((d, i) => {
      last().drop();
      vi.advanceTimersByTime(d - 1);
      expect(FakeWS.instances).toHaveLength(i + 1);
      vi.advanceTimersByTime(1);
      expect(FakeWS.instances).toHaveLength(i + 2);
    });
  });

  it("zera o backoff após abrir com sucesso", () => {
    const s = make();
    s.subscribe("system", () => {});
    last().drop();
    vi.advanceTimersByTime(1000);
    last().drop();
    vi.advanceTimersByTime(2000);
    last().open();
    last().drop();
    vi.advanceTimersByTime(1000);
    expect(FakeWS.instances).toHaveLength(4);
  });

  it("4401 não reconecta e dispara korego:unauthorized", () => {
    const s = make();
    const handler = vi.fn();
    window.addEventListener("korego:unauthorized", handler);
    s.subscribe("system", () => {});
    last().open();
    last().drop(4401);
    vi.advanceTimersByTime(60000);
    window.removeEventListener("korego:unauthorized", handler);
    expect(FakeWS.instances).toHaveLength(1);
    expect(handler).toHaveBeenCalledTimes(1);
    expect(s.status).toBe("closed");
  });

  it("assinaturas têm contagem de referências", () => {
    const s = make();
    const a = vi.fn();
    const b = vi.fn();
    const ua = s.subscribe("system", a);
    const ub = s.subscribe("system", b);
    last().open();
    expect(last().sent).toHaveLength(1);
    last().msg({ type: "event", topic: "system", data: { uptimeSeconds: 5 } });
    expect(a).toHaveBeenCalledWith({ uptimeSeconds: 5 });
    expect(b).toHaveBeenCalledWith({ uptimeSeconds: 5 });
    ua();
    expect(last().sent).toHaveLength(1);
    ub();
    expect(last().sent).toEqual([
      '{"type":"subscribe","topic":"system"}',
      '{"type":"unsubscribe","topic":"system"}',
    ]);
  });

  it("fecha a conexão sem assinaturas e não reconecta", () => {
    const s = make();
    const un = s.subscribe("system", () => {});
    const ws = last();
    ws.open();
    un();
    expect(ws.readyState).toBe(3);
    vi.advanceTimersByTime(60000);
    expect(FakeWS.instances).toHaveLength(1);
    expect(s.status).toBe("closed");
  });
});
