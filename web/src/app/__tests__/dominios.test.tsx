import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import DominiosPage from "../(painel)/dominios/page";

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

const ZERO = "0001-01-01T00:00:00Z";

const domain = (over: Record<string, unknown>) => ({
  id: 1,
  hostname: "loja.exemplo.com",
  targetType: "address",
  appId: null,
  appSlug: "",
  address: "127.0.0.1:5000",
  wwwRedirect: "none",
  forceHttps: true,
  isPanel: false,
  cert: { state: "pending", notAfter: ZERO },
  createdAt: "2026-11-08T12:00:00Z",
  updatedAt: "2026-11-08T12:00:00Z",
  ...over,
});

const panelDomain = domain({
  id: 9,
  hostname: "painel.exemplo.com",
  isPanel: true,
  address: "127.0.0.1:8000",
  cert: { state: "active", notAfter: "2027-02-01T12:00:00Z" },
});

const app = { id: 3, slug: "blog", projectId: 1, domains: [] };

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

type Handler = (init?: { method?: string; body?: string }) => Response;

let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  routes = {
    "GET /api/proxy": () => json({ state: "active", guideUrl: "https://exemplo.com/guia" }),
    "GET /api/domains": () => json({ domains: [panelDomain] }),
    "GET /api/apps": () => json({ apps: [app] }),
    "GET /api/certificates": () => json({ certificates: [] }),
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

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <DominiosPage />
    </QueryClientProvider>,
  );
}

const row = (host: string) =>
  screen.getByRole("link", { name: new RegExp(`^${host.replace(/\./g, "\\.")}`) }).closest("tr") as HTMLElement;

describe("Domínios", () => {
  it("banner bloqueado mostra o processo e o link do guia", async () => {
    routes["GET /api/proxy"] = () =>
      json({ state: "blocked", process: "nginx", guideUrl: "https://exemplo.com/guia" });
    routes["POST /api/proxy/retry"] = () => json({ state: "active", guideUrl: "https://exemplo.com/guia" });
    renderPage();

    expect(
      await screen.findByText("Proxy inativo: as portas 80/443 estão em uso por nginx"),
    ).toBeInTheDocument();
    const guide = screen.getByRole("link", { name: /Guia de migração/ });
    expect(guide).toHaveAttribute("href", "https://exemplo.com/guia");
    expect(guide).toHaveAttribute("target", "_blank");
    expect(guide).toHaveAttribute("rel", "noopener noreferrer");

    fireEvent.click(screen.getByRole("button", { name: "Tentar novamente" }));
    await waitFor(() =>
      expect(screen.queryByText(/Proxy inativo/)).not.toBeInTheDocument(),
    );
    expect(fetchMock).toHaveBeenCalledWith("/api/proxy/retry", expect.objectContaining({ method: "POST" }));
  });

  it("salvar domínio com DNS errado mostra o registro A e mantém na lista", async () => {
    const created = domain({ id: 2 });
    let list = [panelDomain];
    routes["GET /api/domains"] = () => json({ domains: list });
    routes["POST /api/domains"] = () => {
      list = [panelDomain, created];
      return json(
        {
          domain: created,
          dns: {
            ok: false,
            resolved: ["198.51.100.7"],
            expected: ["203.0.113.10"],
            message: "loja.exemplo.com aponta para 198.51.100.7, não para este servidor.",
          },
        },
        201,
      );
    };
    renderPage();
    await screen.findByRole("link", { name: /^painel\.exemplo\.com/ });

    fireEvent.click(screen.getByRole("button", { name: "Adicionar domínio" }));
    const dialog = screen.getByRole("dialog", { name: "Adicionar domínio" });
    fireEvent.change(within(dialog).getByLabelText(/Hostname/), { target: { value: "loja.exemplo.com" } });
    fireEvent.click(within(dialog).getByRole("tab", { name: "Endereço" }));
    const addr = within(dialog).getByRole("textbox", { name: /host:porta/ });
    expect(addr).toHaveAttribute("placeholder", "127.0.0.1:5000");

    // Validação no cliente antes de enviar.
    fireEvent.change(addr, { target: { value: "127.0.0.1" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));
    expect(await within(dialog).findByText(/host:porta/, { selector: "[id$='-address-error']" })).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/domains", expect.objectContaining({ method: "POST" }));

    fireEvent.change(addr, { target: { value: "127.0.0.1:5000" } });
    expect(within(dialog).getByRole("checkbox", { name: /Forçar HTTPS/ })).toBeChecked();
    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));

    // Aviso de DNS com o registro A esperado; o diálogo continua aberto.
    expect(await within(dialog).findByText(/aponta para 198.51.100.7/)).toBeInTheDocument();
    expect(within(dialog).getByText("203.0.113.10")).toBeInTheDocument();
    expect(within(dialog).getAllByText("A").length).toBeGreaterThan(0);
    const sent = JSON.parse(
      (fetchMock.mock.calls.find((c) => c[1]?.method === "POST")?.[1] as { body: string }).body,
    );
    expect(sent).toEqual({
      hostname: "loja.exemplo.com",
      wwwRedirect: "none",
      forceHttps: true,
      routes: [
        { path: "/", targetType: "address", address: "127.0.0.1:5000", maxBodyBytes: 0, noBuffering: false, spa: false },
      ],
      tls: { mode: "auto", certId: null },
      cloudflareOnly: false,
      headers: [],
      compression: true,
    });

    // O domínio já está salvo e aparece na lista.
    await waitFor(() => expect(row("loja.exemplo.com")).toBeInTheDocument());
    fireEvent.click(within(dialog).getByRole("button", { name: "Fechar" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(within(row("loja.exemplo.com")).getByText("Pendente")).toBeInTheDocument();
  });

  it("domínio do painel não pode ser removido; o hostname fica em Configurações", async () => {
    routes["GET /api/domains"] = () =>
      json({
        domains: [
          panelDomain,
          domain({ id: 2, targetType: "app", appId: 3, appSlug: "blog", address: "", cert: { state: "error", notAfter: ZERO, message: "limite de emissão" } }),
        ],
      });
    renderPage();
    await screen.findByRole("link", { name: /^painel\.exemplo\.com/ });

    const p = row("painel.exemplo.com");
    expect(within(p).getByText("Painel")).toBeInTheDocument();
    expect(within(p).getByRole("button", { name: /Editar/ })).toBeInTheDocument();
    expect(within(p).queryByRole("button", { name: /Remover/ })).not.toBeInTheDocument();
    expect(within(p).getByRole("link", { name: /Configurações/ })).toHaveAttribute("href", "/configuracoes/painel/");
    expect(within(p).getByText("Ativo até 01/02/2027")).toBeInTheDocument();

    const l = row("loja.exemplo.com");
    expect(within(l).getByText("Erro: limite de emissão")).toBeInTheDocument();
    expect(within(l).getByRole("link", { name: "blog" })).toBeInTheDocument();
    expect(within(l).getByRole("button", { name: /Editar/ })).toBeInTheDocument();

    // Remover pede confirmação.
    routes["DELETE /api/domains/2"] = () => new Response(null, { status: 204 });
    fireEvent.click(within(l).getByRole("button", { name: /Remover/ }));
    const dialog = screen.getByRole("dialog", { name: /Remover domínio/ });
    fireEvent.click(within(dialog).getByRole("button", { name: "Remover" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith("/api/domains/2", expect.objectContaining({ method: "DELETE" }));
  });
});
