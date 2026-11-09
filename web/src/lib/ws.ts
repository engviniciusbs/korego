"use client";

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { UNAUTHORIZED_EVENT } from "./api";

export type LiveStatus = "connecting" | "open" | "closed";

export interface LiveSocketOptions {
  WebSocketImpl?: typeof WebSocket;
  url?: string;
  random?: () => number;
}

const BASE_DELAY = 1000;
const MAX_DELAY = 30000;
const CLOSE_UNAUTHORIZED = 4401;

export class LiveSocket {
  status: LiveStatus = "closed";

  private ws: WebSocket | null = null;
  private subs = new Map<string, Set<(data: unknown) => void>>();
  private statusCbs = new Set<(s: LiveStatus) => void>();
  private attempt = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private readonly opts: LiveSocketOptions;

  constructor(opts: LiveSocketOptions = {}) {
    this.opts = opts;
  }

  onStatus(cb: (s: LiveStatus) => void): () => void {
    this.statusCbs.add(cb);
    return () => {
      this.statusCbs.delete(cb);
    };
  }

  subscribe(topic: string, cb: (data: unknown) => void): () => void {
    let set = this.subs.get(topic);
    const first = !set;
    if (!set) {
      set = new Set();
      this.subs.set(topic, set);
    }
    set.add(cb);
    if (first) this.send({ type: "subscribe", topic });
    this.ensureConnected();

    let done = false;
    return () => {
      if (done) return;
      done = true;
      const s = this.subs.get(topic);
      if (!s) return;
      s.delete(cb);
      if (s.size === 0) {
        this.subs.delete(topic);
        this.send({ type: "unsubscribe", topic });
        if (this.subs.size === 0) this.shutdown();
      }
    };
  }

  private setStatus(s: LiveStatus) {
    if (this.status === s) return;
    this.status = s;
    this.statusCbs.forEach((cb) => cb(s));
  }

  private send(msg: object) {
    if (this.ws && this.ws.readyState === 1) this.ws.send(JSON.stringify(msg));
  }

  private url(): string {
    if (this.opts.url) return this.opts.url;
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    return `${proto}//${location.host}/api/ws`;
  }

  private ensureConnected() {
    if (this.ws || this.timer) return;
    this.connect();
  }

  private connect() {
    const Impl = this.opts.WebSocketImpl ?? WebSocket;
    this.setStatus("connecting");
    const ws = new Impl(this.url());
    this.ws = ws;
    ws.onopen = () => {
      this.attempt = 0;
      this.setStatus("open");
      for (const topic of this.subs.keys()) this.send({ type: "subscribe", topic });
    };
    ws.onmessage = (e) => {
      let msg: { type?: string; topic?: string; data?: unknown };
      try {
        msg = JSON.parse(e.data as string);
      } catch {
        return;
      }
      if (msg.type === "event" && msg.topic) {
        this.subs.get(msg.topic)?.forEach((cb) => cb(msg.data));
      }
    };
    ws.onclose = (e) => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (e.code === CLOSE_UNAUTHORIZED) {
        this.setStatus("closed");
        if (typeof window !== "undefined") {
          window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
        }
        return;
      }
      this.setStatus("closed");
      if (this.subs.size > 0) this.scheduleReconnect();
    };
    ws.onerror = () => {
      // onclose sempre segue; a reconexão é tratada lá.
    };
  }

  private scheduleReconnect() {
    const random = this.opts.random ?? Math.random;
    const base = Math.min(BASE_DELAY * 2 ** this.attempt, MAX_DELAY);
    const jitter = 1 + (random() * 2 - 1) * 0.2;
    this.attempt++;
    this.timer = setTimeout(() => {
      this.timer = null;
      if (this.subs.size > 0) this.connect();
    }, base * jitter);
  }

  private shutdown() {
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    this.attempt = 0;
    const ws = this.ws;
    this.ws = null;
    ws?.close();
    this.setStatus("closed");
  }
}

let singleton: LiveSocket | null = null;

export function getLiveSocket(): LiveSocket {
  if (!singleton) singleton = new LiveSocket();
  return singleton;
}

/**
 * Entrega cada mensagem do tópico ao callback (useLive guarda só a última).
 * `null` não assina.
 */
export function useLiveStream(topic: string | null, onData: (d: unknown) => void): void {
  const ref = useRef(onData);
  useEffect(() => {
    ref.current = onData;
  });
  useEffect(() => {
    if (!topic) return;
    return getLiveSocket().subscribe(topic, (d) => ref.current(d));
  }, [topic]);
}

export function useLive<T>(topic: string): T | undefined {
  const [data, setData] = useState<T>();
  useEffect(() => {
    return getLiveSocket().subscribe(topic, (d) => setData(d as T));
  }, [topic]);
  return data;
}

const subscribeStatus = (cb: () => void) => getLiveSocket().onStatus(cb);
const getStatus = () => getLiveSocket().status;
const getServerStatus = (): LiveStatus => "closed";

/** Estado atual da conexão em tempo real. */
export function useLiveSocketStatus(): LiveStatus {
  return useSyncExternalStore(subscribeStatus, getStatus, getServerStatus);
}
