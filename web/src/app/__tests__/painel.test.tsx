import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import PainelPage from "../(painel)/configuracoes/painel/page";

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

const panel = (over: Record<string, unknown> = {}) => ({
  domain: "painel.exemplo.com",
  domainId: 9,
  cert: { state: "active", notAfter: "2027-02-01T12:00:00Z" },
  publicPort: true,
  listenAddr: "0.0.0.0:8000",
  requestHTTPS: true,
  requestHost: "painel.exemplo.com",
  ...over,
});

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

type Handler = (init?: { method?: string; body?: string }) => Response;

let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  routes = {
    "GET /api/proxy": () => json({ state: "active", guideUrl: "https://exemplo.com/guia" }),
    "GET /api/settings/panel": () => json(panel()),
  };
  vi.stubGlobal("WebSocket", FakeWS);
  fetchMock = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const h = routes[`${init?.method ?? "GET"} ${url.split("?")[0]}`];
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
      <PainelPage />
    </QueryClientProvider>,
  );
}

describe("Configurações → Painel", () => {
  it("porta 8000 não pode ser desligada fora do domínio com HTTPS", async () => {
    routes["GET /api/settings/panel"] = () =>
      json(panel({ requestHTTPS: false, requestHost: "203.0.113.10" }));
    renderPage();

    const sw = await screen.findByRole("switch", { name: /Porta 8000 pública/ });
    expect(sw).toBeChecked();
    expect(sw).toBeDisabled();
    expect(screen.getByText(/Para desligar, acesse o painel por https:\/\/painel\.exemplo\.com/)).toBeInTheDocument();
    expect(screen.getByText("Ativo até 01/02/2027")).toBeInTheDocument();
  });

  it("certificado pendente mantém o interruptor desabilitado", async () => {
    routes["GET /api/settings/panel"] = () =>
      json(panel({ cert: { state: "pending", notAfter: "0001-01-01T00:00:00Z" } }));
    renderPage();
    const sw = await screen.findByRole("switch", { name: /Porta 8000 pública/ });
    expect(sw).toBeDisabled();
    expect(screen.getByText(/certificado/i, { selector: "#" + sw.getAttribute("aria-describedby")!.replace(/:/g, "\\:") })).toBeInTheDocument();
  });

  it("desligar a porta pede confirmação com o comando de recuperação", async () => {
    routes["PUT /api/settings/panel/public-port"] = () => json(panel({ publicPort: false }));
    renderPage();

    const sw = await screen.findByRole("switch", { name: /Porta 8000 pública/ });
    expect(sw).toBeEnabled();
    fireEvent.click(sw);

    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveTextContent(
      "Se o domínio deixar de funcionar, reative pelo servidor com korego painel --porta-publica.",
    );
    expect(within(dialog).getByText("korego painel --porta-publica").tagName).toBe("CODE");
    expect(fetchMock).not.toHaveBeenCalledWith("/api/settings/panel/public-port", expect.anything());

    routes["GET /api/settings/panel"] = () => json(panel({ publicPort: false }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Desligar porta 8000" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const call = fetchMock.mock.calls.find((c) => c[0] === "/api/settings/panel/public-port");
    expect(JSON.parse((call?.[1] as { body: string }).body)).toEqual({ enabled: false });

    // Com a porta desligada, o domínio fica só leitura.
    await waitFor(() => expect(screen.getByRole("switch", { name: /Porta 8000 pública/ })).not.toBeChecked());
    expect(screen.getByLabelText(/Domínio do painel/)).toHaveAttribute("readonly");
    expect(screen.getByText("Reative a porta 8000 pública antes de alterar o domínio.")).toBeInTheDocument();

    // Religar não pede confirmação.
    routes["PUT /api/settings/panel/public-port"] = () => json(panel());
    fireEvent.click(screen.getByRole("switch", { name: /Porta 8000 pública/ }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() =>
      expect(fetchMock.mock.calls.filter((c) => c[0] === "/api/settings/panel/public-port")).toHaveLength(2),
    );
  });

  it("salvo com falha do proxy mostra o aviso", async () => {
    routes["GET /api/settings/panel"] = () => json(panel({ domain: "", domainId: 0, cert: { state: "pending", notAfter: "0001-01-01T00:00:00Z" } }));
    routes["PUT /api/settings/panel/domain"] = () => json({ saved: true, proxyError: "caddy recusou a configuração" });
    renderPage();
    const input = await screen.findByLabelText(/Domínio do painel/);
    fireEvent.change(input, { target: { value: "painel.exemplo.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar domínio" }));
    expect(
      await screen.findByText("Salvo, mas o proxy não aplicou: caddy recusou a configuração"),
    ).toBeInTheDocument();
  });
});
