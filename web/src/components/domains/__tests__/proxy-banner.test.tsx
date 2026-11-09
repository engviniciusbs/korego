import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import DominiosPage from "@/app/(painel)/dominios/page";
import { ProxyBanner, proxyBannerTitle } from "../proxy-banner";

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
}

function push(topic: string, data: unknown) {
  act(() => {
    for (const ws of FakeWS.instances) ws.onmessage?.({ data: JSON.stringify({ type: "event", topic, data }) });
  });
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const fail = (code: string, message: string, status: number) => json({ error: { code, message } }, status);

const GUIDE = "https://exemplo.com/guia";

const proxy = (over: Record<string, unknown> = {}) => ({
  state: "trial",
  mode: "trial",
  process: "nginx (pid 1402)",
  guideUrl: GUIDE,
  migratedFrom: null,
  migratedAt: null,
  lastMigration: null,
  migrating: false,
  rollbackAvailable: false,
  ...over,
});

const site = (host: string, over: Record<string, unknown> = {}) => ({
  host,
  auto: false,
  ok: true,
  pending: false,
  tlsOk: true,
  leafOk: true,
  paths: [{ path: "/", static: false, ok: true, status: 200, index: false }],
  leaf: { names: [host], notAfter: "2027-06-01T00:00:00Z", issuer: "CA Exemplo" },
  ...over,
});

const okSite = site("loja.exemplo.com");
const pendingSite = site("exemplo.com", {
  auto: true,
  ok: true, // como o backend: auto sem certificado no ensaio passa como pending
  pending: true,
  tlsOk: false,
  leafOk: false,
  paths: [],
  leaf: { names: [] },
});
const badSite = site("api.exemplo.net", {
  ok: false,
  paths: [{ path: "/", static: false, ok: false, status: 502, index: false }],
  message: "/: resposta 502",
});

type Handler = (init?: { method?: string; body?: string }) => Response;

let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  FakeWS.instances = [];
  routes = {
    "GET /api/proxy": () => json(proxy()),
    "GET /api/proxy/trial-check": () => json({ sites: [okSite, pendingSite] }),
  };
  vi.stubGlobal("WebSocket", FakeWS);
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

function renderBanner() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ProxyBanner detailed />
    </QueryClientProvider>,
  );
}

const calls = (key: string) =>
  fetchMock.mock.calls.filter(([url, init]) => `${init?.method ?? "GET"} ${String(url).split("?")[0]}` === key);

const checkItem = (host: string) =>
  screen.getByText(host, { selector: "[data-host]" }).closest("li") as HTMLElement;

describe("ProxyBanner — ensaio e troca de servidor", () => {
  it("ensaio mostra processo, checklist por domínio (ok, pendente, falha) e botão Assumir as portas", async () => {
    routes["GET /api/proxy/trial-check"] = () => json({ sites: [okSite, pendingSite, badSite] });
    renderBanner();

    expect(
      await screen.findByText("Ensaio: o Caddy roda em portas de teste enquanto nginx (pid 1402) ocupa 80/443"),
    ).toBeInTheDocument();

    await screen.findByText("loja.exemplo.com", { selector: "[data-host]" });
    expect(checkItem("loja.exemplo.com")).toHaveAttribute("data-result", "ok");
    expect(within(checkItem("loja.exemplo.com")).getByText("ok")).toBeInTheDocument();

    expect(checkItem("exemplo.com")).toHaveAttribute("data-result", "pending");
    expect(within(checkItem("exemplo.com")).getByText("certificado será emitido após assumir")).toBeInTheDocument();

    expect(checkItem("api.exemplo.net")).toHaveAttribute("data-result", "failed");
    expect(within(checkItem("api.exemplo.net")).getByText("/: resposta 502")).toBeInTheDocument();

    expect(screen.getByRole("button", { name: "Assumir as portas" })).toBeInTheDocument();
  });

  it("Assumir pede código 2FA, mostra passos ao vivo e termina em sucesso", async () => {
    let attempts = 0;
    routes["POST /api/proxy/takeover"] = () => {
      attempts++;
      return attempts === 1
        ? fail("INVALID_CODE", "Código inválido.", 403)
        : json({ status: "running", topic: "proxy-migration" }, 202);
    };
    renderBanner();

    const btn = await screen.findByRole("button", { name: "Assumir as portas" });
    await waitFor(() => expect(btn).toBeEnabled());
    fireEvent.click(btn);

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/para o nginx/)).toBeInTheDocument();
    expect(within(dialog).getByText(/80\/443/)).toBeInTheDocument();
    expect(within(dialog).getByText(/volta automaticamente/i)).toBeInTheDocument();
    const code = within(dialog).getByLabelText(/Código/);
    expect(code).toHaveFocus();

    // Código vazio não chega ao servidor.
    fireEvent.click(within(dialog).getByRole("button", { name: "Assumir as portas" }));
    expect(await within(dialog).findByText(/6 dígitos do autenticador ou um código de recuperação/)).toBeInTheDocument();
    expect(calls("POST /api/proxy/takeover")).toHaveLength(0);

    // Código errado: o erro aparece no campo.
    fireEvent.change(code, { target: { value: "111111" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Assumir as portas" }));
    expect(await within(dialog).findByText("Código inválido.")).toBeInTheDocument();
    expect(code).toHaveAttribute("aria-invalid", "true");

    fireEvent.change(code, { target: { value: "123456" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Assumir as portas" }));
    await waitFor(() => expect(calls("POST /api/proxy/takeover")).toHaveLength(2));
    expect(JSON.parse(calls("POST /api/proxy/takeover")[1][1].body)).toEqual({ code: "123456" });

    const steps = await within(dialog).findByRole("list", { name: "Passos da troca" });
    expect(steps.closest("[aria-live]")).toHaveAttribute("aria-live", "polite");

    push("proxy-migration", { op: "takeover", step: "stop", status: "running", message: "parando o nginx" });
    expect(within(steps).getByText("parando o nginx")).toBeInTheDocument();
    push("proxy-migration", { op: "takeover", step: "stop", status: "ok", message: "nginx parado" });
    push("proxy-migration", { op: "takeover", step: "bind", status: "running", message: "Caddy assumindo as portas 80/443" });
    const items = within(steps).getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveAttribute("data-status", "ok");
    expect(items[0]).toHaveTextContent("nginx parado");
    expect(items[1]).toHaveAttribute("data-status", "running");

    routes["GET /api/proxy"] = () =>
      json(
        proxy({
          state: "active",
          mode: "normal",
          process: "",
          migratedFrom: "nginx",
          migratedAt: "2026-11-09T15:00:00Z",
          rollbackAvailable: true,
          lastMigration: { op: "takeover", outcome: "ok", message: "pronto", at: "2026-11-09T15:00:00Z" },
        }),
      );
    const before = calls("GET /api/proxy").length;
    push("proxy-migration", { op: "takeover", message: "pronto", done: true, result: "ok" });

    expect(await within(dialog).findByText("Migrado do nginx")).toBeInTheDocument();
    // O evento final relê o estado do proxy.
    await waitFor(() => expect(calls("GET /api/proxy").length).toBeGreaterThan(before));
    fireEvent.click(within(dialog).getByRole("button", { name: "Fechar" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(await screen.findByRole("button", { name: "Voltar para o nginx" })).toBeInTheDocument();
  });

  it("envio duplo não repete o POST e o código é limpo ao enviar", async () => {
    let release: (r: Response) => void = () => {};
    routes["POST /api/proxy/takeover"] = () => {
      throw new Error("não usado");
    };
    fetchMock.mockImplementation(async (url: string, init?: { method?: string; body?: string }) => {
      const key = `${init?.method ?? "GET"} ${url.split("?")[0]}`;
      if (key === "POST /api/proxy/takeover") return new Promise<Response>((r) => (release = r));
      const h = routes[key];
      return h ? h(init) : json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
    });
    renderBanner();
    const btn = await screen.findByRole("button", { name: "Assumir as portas" });
    await waitFor(() => expect(btn).toBeEnabled());
    fireEvent.click(btn);
    const dialog = await screen.findByRole("dialog");
    const code = within(dialog).getByLabelText(/Código/);
    fireEvent.change(code, { target: { value: "123456" } });
    const form = dialog.querySelector("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    await waitFor(() => expect(calls("POST /api/proxy/takeover")).toHaveLength(1));
    expect(code).toHaveValue("");
    release(json({ status: "running", topic: "proxy-migration" }, 202));
    await within(dialog).findByRole("list", { name: "Passos da troca" });
    expect(calls("POST /api/proxy/takeover")).toHaveLength(1);
  });

  it("resultado rolled_back mostra 'voltou para o nginx: <motivo>'", async () => {
    routes["POST /api/proxy/takeover"] = () => json({ status: "running", topic: "proxy-migration" }, 202);
    renderBanner();

    const btn = await screen.findByRole("button", { name: "Assumir as portas" });
    await waitFor(() => expect(btn).toBeEnabled());
    fireEvent.click(btn);
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText(/Código/), { target: { value: "123456" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Assumir as portas" }));
    await within(dialog).findByRole("list", { name: "Passos da troca" });

    push("proxy-migration", { op: "takeover", step: "verify", status: "failed", message: "loja.exemplo.com: 502" });
    push("proxy-migration", {
      op: "takeover",
      done: true,
      result: "rolled_back",
      message: "voltou para o nginx: a verificação falhou: loja.exemplo.com: 502",
    });
    expect(
      await within(dialog).findByText("voltou para o nginx: a verificação falhou: loja.exemplo.com: 502"),
    ).toBeInTheDocument();
  });

  it("resultado failed mostra a mensagem em alerta vermelho com o guia", async () => {
    routes["GET /api/proxy"] = () =>
      json(proxy({ lastMigration: { op: "takeover", outcome: "failed", message: "deu ruim", at: "2026-11-09T15:00:00Z" } }));
    renderBanner();
    const alert = (await screen.findByText("deu ruim")).closest("[data-variant]") as HTMLElement;
    expect(alert).toHaveAttribute("data-variant", "danger");
    expect(within(alert).getByRole("link", { name: /Guia de migração/ })).toHaveAttribute("href", GUIDE);
  });

  it("migrado mostra 'Migrado do nginx em <data>' e Voltar para o nginx", async () => {
    routes["GET /api/proxy"] = () =>
      json(
        proxy({
          state: "active",
          mode: "normal",
          process: "",
          migratedFrom: "nginx",
          migratedAt: "2026-11-09T15:00:00Z",
          rollbackAvailable: true,
        }),
      );
    routes["POST /api/proxy/rollback"] = () => json({ status: "running", topic: "proxy-migration" }, 202);
    renderBanner();

    expect(await screen.findByText(/^Migrado do nginx em \d{2}\/11\/2026/)).toBeInTheDocument();
    const back = screen.getByRole("button", { name: "Voltar para o nginx" });
    expect(back).toBeEnabled();
    expect(calls("GET /api/proxy/trial-check")).toHaveLength(0);

    fireEvent.click(back);
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/ensaio/)).toBeInTheDocument();
    fireEvent.change(within(dialog).getByLabelText(/Código/), { target: { value: "abcde-12345" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Voltar para o nginx" }));
    await waitFor(() => expect(calls("POST /api/proxy/rollback")).toHaveLength(1));
    expect(JSON.parse(calls("POST /api/proxy/rollback")[0][1].body)).toEqual({ code: "abcde-12345" });
  });

  it("Assumir desabilitado quando algum domínio falha no ensaio", async () => {
    routes["GET /api/proxy/trial-check"] = () => json({ sites: [okSite, pendingSite, badSite] });
    renderBanner();
    await screen.findByText("api.exemplo.net", { selector: "[data-host]" });
    const btn = screen.getByRole("button", { name: "Assumir as portas" });
    expect(btn).toBeDisabled();
    expect(screen.getByText(/corrija os domínios com falha/i)).toBeInTheDocument();
  });

  it("Assumir desabilitado durante uma troca e o estado é relido a cada 5 s", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      routes["GET /api/proxy"] = () => json(proxy({ migrating: true }));
      renderBanner();
      expect(await screen.findByText("Troca de servidor em andamento")).toBeInTheDocument();
      const btn = screen.getByRole("button", { name: "Assumir as portas" });
      expect(btn).toBeDisabled();
      const before = calls("GET /api/proxy").length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5100);
      });
      expect(calls("GET /api/proxy").length).toBeGreaterThan(before);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("Domínios durante a troca de servidor", () => {
  it("diálogo de domínio aberto desabilita Salvar quando a troca começa", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    routes["GET /api/proxy"] = () => json(proxy());
    routes["GET /api/domains"] = () => json({ domains: [] });
    routes["GET /api/apps"] = () => json({ apps: [] });
    routes["GET /api/certificates"] = () => json({ certificates: [] });
    render(
      <QueryClientProvider client={client}>
        <DominiosPage />
      </QueryClientProvider>,
    );
    const add = await screen.findByRole("button", { name: "Adicionar domínio" });
    await waitFor(() => expect(add).toBeEnabled());
    fireEvent.click(add);
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: "Salvar" })).toBeEnabled();

    act(() => {
      client.setQueryData(["proxy"], proxy({ migrating: true }));
    });
    // O TanStack Query notifica de forma assíncrona.
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Salvar" })).toBeDisabled());
    expect(within(dialog).getByText(/Troca de servidor em andamento/)).toBeInTheDocument();
    // Enter no formulário também não grava.
    fireEvent.submit(dialog.querySelector("form")!);
    expect(calls("POST /api/domains")).toHaveLength(0);
  });

  it("bloqueia Adicionar, Editar e Remover enquanto a troca está em andamento", async () => {
    const d = {
      id: 1,
      hostname: "loja.exemplo.com",
      targetType: "address",
      appId: null,
      appSlug: "",
      address: "127.0.0.1:5000",
      wwwRedirect: "none",
      forceHttps: true,
      isPanel: false,
      routes: [{ path: "/", targetType: "address", address: "127.0.0.1:5000", maxBodyBytes: 0, noBuffering: false, spa: false }],
      tls: { mode: "auto", certId: null, expiresSoon: false },
      cloudflareOnly: false,
      headers: [],
      compression: true,
      cert: { state: "pending", notAfter: "0001-01-01T00:00:00Z" },
      createdAt: "2026-11-08T12:00:00Z",
      updatedAt: "2026-11-08T12:00:00Z",
    };
    routes["GET /api/proxy"] = () => json(proxy({ migrating: true }));
    routes["GET /api/domains"] = () => json({ domains: [d] });
    routes["GET /api/apps"] = () => json({ apps: [] });
    routes["GET /api/certificates"] = () => json({ certificates: [] });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <DominiosPage />
      </QueryClientProvider>,
    );
    await screen.findAllByText("Troca de servidor em andamento");
    await waitFor(() => expect(screen.getByRole("button", { name: "Adicionar domínio" })).toBeDisabled());
    const row = (await screen.findByRole("link", { name: /^loja\.exemplo\.com/ })).closest("tr") as HTMLElement;
    expect(within(row).getByRole("button", { name: /Editar/ })).toBeDisabled();
    expect(within(row).getByRole("button", { name: /Remover/ })).toBeDisabled();
  });
});

describe("proxyBannerTitle — ensaio com as portas livres", () => {
  it("não diz que outro processo ocupa 80/443", () => {
    const title = proxyBannerTitle({ state: "trial", mode: "trial", message: "As portas 80/443 estão livres.", guideUrl: GUIDE });
    expect(title).toBe("Ensaio: o Caddy roda em portas de teste e as portas 80/443 estão livres");
    expect(proxyBannerTitle({ state: "trial", mode: "trial", process: "nginx (pid 7)", guideUrl: GUIDE })).toBe(
      "Ensaio: o Caddy roda em portas de teste enquanto nginx (pid 7) ocupa 80/443",
    );
  });
});
