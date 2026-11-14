import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import TokensPage from "../(painel)/configuracoes/tokens/page";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

type Handler = (init?: { method?: string; body?: string }) => Response;
let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

const item = (over: Record<string, unknown> = {}) => ({
  id: 1,
  name: "claude-code",
  prefix: "kg_AbCd",
  createdAt: "2026-11-01T12:00:00Z",
  lastUsedAt: null,
  lastUsedIp: "",
  expiresAt: null,
  kind: "manual",
  ...over,
});

beforeEach(() => {
  routes = {
    "GET /api/tokens": () => json({ items: [item()] }),
    "GET /api/settings/panel": () => json({ domain: "p.exemplo.com", cert: { state: "active" } }),
  };
  fetchMock = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const h = routes[`${init?.method ?? "GET"} ${url.split("?")[0]}`];
    return h ? h(init) : json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <TokensPage />
    </QueryClientProvider>,
  );
}

async function openCreate() {
  await screen.findByText("claude-code");
  fireEvent.click(screen.getByRole("button", { name: "Novo token" }));
  return await screen.findByRole("dialog");
}

describe("Configurações → Tokens de API", () => {
  it("lista nome, prefixo, último uso e expiração (nunca quando nulos)", async () => {
    routes["GET /api/tokens"] = () =>
      json({
        items: [
          item(),
          item({ id: 2, name: "ci", prefix: "kg_Zz9y", lastUsedAt: "2026-11-10T10:00:00Z", expiresAt: "2027-01-01T00:00:00Z" }),
        ],
      });
    renderPage();
    const row1 = (await screen.findByText("claude-code")).closest("tr")!;
    expect(within(row1).getByText("kg_AbCd…")).toBeInTheDocument();
    expect(within(row1).getAllByText("nunca")).toHaveLength(2);
    const row2 = screen.getByText("ci").closest("tr")!;
    expect(within(row2).queryByText("nunca")).toBeNull();
  });

  it("cria o token, mostra uma vez com o comando e descarta ao fechar", async () => {
    let sent: Record<string, unknown> = {};
    routes["POST /api/tokens"] = (init) => {
      sent = JSON.parse(init!.body!);
      return json({ token: "kg_SEGREDO123", item: item({ id: 5, name: "novo" }) }, 201);
    };
    renderPage();
    const dlg = await openCreate();
    expect(within(dlg).getByLabelText("Validade")).toHaveValue("90");
    fireEvent.change(within(dlg).getByLabelText(/^Nome/), { target: { value: "novo" } });
    fireEvent.change(within(dlg).getByLabelText(/^Código 2FA/), { target: { value: "123456" } });
    fireEvent.click(within(dlg).getByRole("button", { name: "Criar token" }));
    await waitFor(() => expect(screen.getByText("kg_SEGREDO123")).toBeInTheDocument());
    expect(sent).toEqual({ name: "novo", expiresInDays: 90, code: "123456" });
    expect(
      screen.getByText(
        `claude mcp add --transport http korego ${location.origin}/mcp --header "Authorization: Bearer kg_SEGREDO123"`,
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Copiar token" })).toBeInTheDocument();
    fireEvent.click(within(dlg).getByRole("tab", { name: "Codex" }));
    expect(screen.getByText(/\[mcp_servers\.korego\]/)).toHaveTextContent(
      `url = "${location.origin}/mcp" http_headers = { "Authorization" = "Bearer kg_SEGREDO123" }`,
    );
    fireEvent.click(within(dlg).getByRole("tab", { name: "Cursor" }));
    const link = screen.getByRole("link", { name: "Abrir no Cursor" }).getAttribute("href")!;
    expect(link).toMatch(/^cursor:\/\/anysphere\.cursor-deeplink\/mcp\/install\?name=korego&config=/);
    const cfg = JSON.parse(atob(decodeURIComponent(link.split("config=")[1])));
    expect(cfg).toEqual({ url: `${location.origin}/mcp`, headers: { Authorization: "Bearer kg_SEGREDO123" } });
    fireEvent.click(screen.getByRole("button", { name: "Fechar" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await openCreate();
    expect(screen.queryByText("kg_SEGREDO123")).toBeNull();
  });

  it("TOKEN_NAME_TAKEN mostra o erro no nome e limpa o código", async () => {
    routes["POST /api/tokens"] = () => json({ error: { code: "TOKEN_NAME_TAKEN", message: "Já existe" } }, 409);
    renderPage();
    const dlg = await openCreate();
    fireEvent.change(within(dlg).getByLabelText(/^Nome/), { target: { value: "claude-code" } });
    fireEvent.change(within(dlg).getByLabelText(/^Código 2FA/), { target: { value: "123456" } });
    fireEvent.click(within(dlg).getByRole("button", { name: "Criar token" }));
    expect(await within(dlg).findByText("Já existe um token com esse nome.")).toBeInTheDocument();
    expect(within(dlg).getByLabelText(/^Código 2FA/)).toHaveValue("");
  });

  it("marca como expirado o token cuja validade já passou", async () => {
    routes["GET /api/tokens"] = () =>
      json({ items: [item({ expiresAt: "2020-01-01T00:00:00Z" }), item({ id: 2, name: "vivo", expiresAt: "2999-01-01T00:00:00Z" })] });
    renderPage();
    const old = (await screen.findByText("claude-code")).closest("tr")!;
    expect(within(old).getByText("expirado")).toBeInTheDocument();
    expect(within(screen.getByText("vivo").closest("tr")!).queryByText("expirado")).toBeNull();
  });

  it("TOKEN_REQUIRES_HTTPS mostra a mensagem e o link para o painel", async () => {
    routes["POST /api/tokens"] = () =>
      json({ error: { code: "TOKEN_REQUIRES_HTTPS", message: "Tokens de API só funcionam via HTTPS." } }, 403);
    renderPage();
    const dlg = await openCreate();
    fireEvent.change(within(dlg).getByLabelText(/^Nome/), { target: { value: "x" } });
    fireEvent.change(within(dlg).getByLabelText(/^Código 2FA/), { target: { value: "123456" } });
    fireEvent.click(within(dlg).getByRole("button", { name: "Criar token" }));
    expect(await within(dlg).findByText(/só funcionam via HTTPS/)).toBeInTheDocument();
    expect(within(dlg).getByRole("link", { name: /Configurações → Painel/ })).toHaveAttribute(
      "href",
      "/configuracoes/painel/",
    );
  });

  it("revogar pede confirmação e recarrega a lista", async () => {
    let revoked = false;
    routes["GET /api/tokens"] = () => json({ items: revoked ? [] : [item()] });
    routes["DELETE /api/tokens/1"] = () => {
      revoked = true;
      return new Response(null, { status: 204 });
    };
    renderPage();
    await screen.findByText("claude-code");
    fireEvent.click(screen.getByRole("button", { name: "Revogar token claude-code" }));
    expect(fetchMock.mock.calls.some((c) => c[1]?.method === "DELETE")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Confirmar" }));
    await waitFor(() => expect(screen.queryByText("claude-code")).toBeNull());
    expect(screen.getByText("Nenhum token criado.")).toBeInTheDocument();
  });

  it("com o painel ativo, mostra Conectar um agente com o comando sem token", async () => {
    renderPage();
    expect(await screen.findByText("Conectar um agente")).toBeInTheDocument();
    expect(await screen.findByText("claude mcp add --transport http korego https://p.exemplo.com/mcp")).toBeInTheDocument();
    expect(screen.getByText("Informe só a URL ao agente: o navegador abre para você autorizar com o 2FA.")).toBeInTheDocument();
    expect(screen.getByText("Use exatamente esta URL; outro endereço do mesmo servidor não autoriza.")).toBeInTheDocument();
    // O título do painel não se repete no rótulo das abas.
    expect(screen.getAllByText("Conectar um agente")).toHaveLength(1);
    expect(screen.getByText("Cliente")).toBeInTheDocument();
  });

  it("falha ao ler a configuração do painel mostra o erro, não o alerta de domínio", async () => {
    routes["GET /api/settings/panel"] = () => json({ error: { code: "INTERNAL", message: "Erro interno." } }, 500);
    renderPage();
    expect(await screen.findByText("Não foi possível carregar a configuração do painel.")).toBeInTheDocument();
    expect(screen.queryByText("Configure o domínio do painel com HTTPS para conectar agentes.")).toBeNull();
    expect(screen.queryByText(/claude mcp add/)).toBeNull();
  });

  it("sem certificado ativo, mostra o alerta e o link para Painel", async () => {
    routes["GET /api/settings/panel"] = () => json({ domain: "p.exemplo.com", cert: { state: "pending" } });
    renderPage();
    expect(await screen.findByText("Configure o domínio do painel com HTTPS para conectar agentes.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Configurações → Painel/ })).toHaveAttribute("href", "/configuracoes/painel/");
    expect(screen.queryByText(/claude mcp add/)).toBeNull();
  });

  it("marca a conexão OAuth com o selo OAuth", async () => {
    routes["GET /api/tokens"] = () => json({ items: [item({ id: 3, name: "agente", kind: "oauth" }), item()] });
    renderPage();
    const row = (await screen.findByText("agente")).closest("tr")!;
    expect(within(row).getByText("OAuth")).toBeInTheDocument();
    expect(within(screen.getByText("claude-code").closest("tr")!).queryByText("OAuth")).toBeNull();
  });
});
