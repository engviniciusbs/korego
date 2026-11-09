import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import DominiosPage from "@/app/(painel)/dominios/page";

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

const root = (over: Record<string, unknown> = {}) => ({
  path: "/",
  targetType: "address",
  address: "127.0.0.1:5000",
  maxBodyBytes: 0,
  noBuffering: false,
  spa: false,
  ...over,
});

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
  routes: [root()],
  tls: { mode: "auto", certId: null, expiresSoon: false },
  cloudflareOnly: false,
  headers: [],
  compression: true,
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
  routes: [root({ address: "127.0.0.1:8000" })],
  compression: false,
  cert: { state: "active", notAfter: "2027-02-01T12:00:00Z" },
});

const curinga = {
  id: 4,
  name: "curinga",
  issuer: "CA Exemplo",
  names: ["*.exemplo.com", "exemplo.com"],
  notAfter: "2027-06-01T00:00:00Z",
  fingerprint: "AB:CD:EF",
  usedBy: [],
  expiresSoon: false,
};

const app = { id: 3, slug: "blog", projectId: 1, domains: [] };

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const fail = (code: string, message: string, field?: string, status = 400) =>
  json({ error: { code, message, field } }, status);

const okSave = (d: unknown) => json({ domain: d, dns: { ok: true, resolved: [], expected: [], message: "" } }, 201);

type Handler = (init?: { method?: string; body?: string }) => Response;

let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  routes = {
    "GET /api/proxy": () => json({ state: "active", guideUrl: "" }),
    "GET /api/domains": () => json({ domains: [panelDomain] }),
    "GET /api/apps": () => json({ apps: [app] }),
    "GET /api/certificates": () => json({ certificates: [curinga] }),
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

/** Corpo JSON da última chamada com o método e a URL dados. */
function sentBody(method: string, url: string) {
  const call = [...fetchMock.mock.calls].reverse().find((c) => c[0] === url && c[1]?.method === method);
  if (!call) throw new Error(`sem ${method} ${url}`);
  return JSON.parse((call[1] as { body: string }).body);
}

async function openNew() {
  renderPage();
  await screen.findByRole("link", { name: /^painel\.exemplo\.com/ });
  fireEvent.click(screen.getByRole("button", { name: "Adicionar domínio" }));
  const dialog = screen.getByRole("dialog", { name: "Adicionar domínio" });
  fireEvent.change(within(dialog).getByLabelText(/Hostname/), { target: { value: "loja.exemplo.com" } });
  return dialog;
}

const tab = (dialog: HTMLElement, name: string) =>
  fireEvent.click(within(dialog).getByRole("tab", { name }));

const routeGroups = (dialog: HTMLElement) =>
  within(dialog)
    .getAllByRole("group", { name: /^Rota / })
    .map((g) => g.getAttribute("aria-label"));

describe("Editor de domínio", () => {
  it("adiciona, exibe na ordem de precedência e remove rotas; '/' é obrigatória", async () => {
    routes["POST /api/domains"] = () => okSave(domain({ id: 2 }));
    const dialog = await openNew();
    tab(dialog, "Endereço");
    fireEvent.change(within(dialog).getByRole("textbox", { name: /host:porta/ }), {
      target: { value: "127.0.0.1:5000" },
    });

    // Duas rotas por caminho.
    fireEvent.click(within(dialog).getByRole("button", { name: "Adicionar rota por caminho" }));
    let path = within(dialog).getAllByRole("textbox", { name: "Caminho" });
    expect(path).toHaveLength(1);
    fireEvent.change(path[0], { target: { value: "/a/" } });
    fireEvent.blur(path[0]);
    fireEvent.click(within(dialog).getByRole("button", { name: "Adicionar rota" }));
    path = within(dialog).getAllByRole("textbox", { name: "Caminho" });
    const fresh = path.find((p) => (p as HTMLInputElement).value === "") as HTMLElement;
    fireEvent.change(fresh, { target: { value: "/api/v1/" } });
    fireEvent.blur(fresh);

    // Ordem de precedência: o prefixo mais longo primeiro, "/" por último.
    expect(routeGroups(dialog)).toEqual(["Rota /api/v1/", "Rota /a/", "Rota /"]);

    // "/" não pode ser removida; as outras sim.
    const rootGroup = within(dialog).getByRole("group", { name: "Rota /" });
    expect(within(rootGroup).queryByRole("button", { name: /Remover/ })).not.toBeInTheDocument();
    const a = within(dialog).getByRole("group", { name: "Rota /a/" });
    fireEvent.click(within(a).getByRole("button", { name: /Remover/ }));
    expect(routeGroups(dialog)).toEqual(["Rota /api/v1/", "Rota /"]);

    // A rota nova precisa de destino válido antes de enviar.
    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));
    expect(await within(dialog).findByText("Escolha a aplicação.")).toBeInTheDocument();
    const v1 = within(dialog).getByRole("group", { name: "Rota /api/v1/" });
    fireEvent.click(within(v1).getByRole("tab", { name: "Endereço" }));
    fireEvent.change(within(v1).getByRole("textbox", { name: /host:porta/ }), {
      target: { value: "127.0.0.1:6000" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(sentBody("POST", "/api/domains").routes).toEqual([
      { path: "/api/v1/", targetType: "address", address: "127.0.0.1:6000", maxBodyBytes: 0, noBuffering: false, spa: false },
      { path: "/", targetType: "address", address: "127.0.0.1:5000", maxBodyBytes: 0, noBuffering: false, spa: false },
    ]);
  });

  it("opções aparecem conforme o tipo (endereço: limite e sem buffer; estático: pasta, SPA e cache imutável)", async () => {
    routes["POST /api/domains"] = () => okSave(domain({ id: 2 }));
    const dialog = await openNew();

    tab(dialog, "Endereço");
    expect(within(dialog).getByRole("spinbutton", { name: /Limite do corpo/ })).toBeInTheDocument();
    expect(within(dialog).getByRole("checkbox", { name: /Sem buffer/ })).toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: /^Pasta/ })).not.toBeInTheDocument();
    fireEvent.change(within(dialog).getByRole("spinbutton", { name: /Limite do corpo/ }), { target: { value: "10" } });

    tab(dialog, "Pasta");
    expect(within(dialog).queryByRole("spinbutton", { name: /Limite do corpo/ })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("checkbox", { name: /Sem buffer/ })).not.toBeInTheDocument();
    fireEvent.change(within(dialog).getByRole("textbox", { name: /^Pasta/ }), { target: { value: "/var/www/loja" } });
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /SPA/ }));
    fireEvent.change(within(dialog).getByRole("textbox", { name: /Cache imutável/ }), { target: { value: "/assets/" } });

    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(sentBody("POST", "/api/domains").routes).toEqual([
      {
        path: "/",
        targetType: "static",
        root: "/var/www/loja",
        spa: true,
        immutablePrefix: "/assets/",
        maxBodyBytes: 0,
        noBuffering: false,
      },
    ]);
  });

  it("modelo segurança preenche os 4 cabeçalhos exatos", async () => {
    routes["POST /api/domains"] = () => okSave(domain({ id: 2 }));
    const dialog = await openNew();
    tab(dialog, "Endereço");
    fireEvent.change(within(dialog).getByRole("textbox", { name: /host:porta/ }), {
      target: { value: "127.0.0.1:5000" },
    });

    tab(dialog, "Segurança");
    expect(within(dialog).getByRole("switch", { name: /Compressão/ })).toBeChecked();
    expect(within(dialog).getByRole("switch", { name: /Só Cloudflare/ })).not.toBeChecked();
    fireEvent.click(within(dialog).getByRole("button", { name: "Modelo segurança" }));

    const expected = [
      { name: "Strict-Transport-Security", value: "max-age=31536000" },
      { name: "X-Content-Type-Options", value: "nosniff" },
      { name: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
      { name: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()" },
    ];
    expected.forEach((h, i) => {
      expect(within(dialog).getByRole("textbox", { name: `Nome do cabeçalho ${i + 1}` })).toHaveValue(h.name);
      expect(within(dialog).getByRole("textbox", { name: `Valor do cabeçalho ${i + 1}` })).toHaveValue(h.value);
    });
    // Aplicar de novo não duplica.
    fireEvent.click(within(dialog).getByRole("button", { name: "Modelo segurança" }));
    expect(within(dialog).queryByRole("textbox", { name: "Nome do cabeçalho 5" })).not.toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const body = sentBody("POST", "/api/domains");
    expect(body.headers).toEqual(expected);
    expect(body.compression).toBe(true);
    expect(body.cloudflareOnly).toBe(false);
  });

  it("certificado próprio: escolher existente ou adicionar colando PEM; a resposta nunca mostra chave", async () => {
    const added = { ...curinga, id: 7, name: "loja", names: ["loja.exemplo.com"], issuer: "CA Loja", fingerprint: "12:34" };
    let stored = [curinga];
    routes["GET /api/certificates"] = () => json({ certificates: stored });
    routes["POST /api/certificates"] = () => {
      stored = [curinga, added];
      return json({ certificate: added }, 201);
    };
    routes["POST /api/domains"] = () => okSave(domain({ id: 2 }));
    const dialog = await openNew();
    tab(dialog, "Endereço");
    fireEvent.change(within(dialog).getByRole("textbox", { name: /host:porta/ }), {
      target: { value: "127.0.0.1:5000" },
    });

    tab(dialog, "Certificado");
    expect(within(dialog).getByRole("radio", { name: /Automático/ })).toBeChecked();
    fireEvent.click(within(dialog).getByRole("radio", { name: /Próprio/ }));

    // Escolher um existente mostra o resumo.
    const select = await within(dialog).findByRole("combobox", { name: /^Certificado/ });
    await within(select).findByRole("option", { name: /curinga/ });
    fireEvent.change(select, { target: { value: "4" } });
    expect(within(dialog).getByText("CA Exemplo")).toBeInTheDocument();
    expect(within(dialog).getByText("*.exemplo.com, exemplo.com")).toBeInTheDocument();

    // Adicionar colando PEM.
    fireEvent.click(within(dialog).getByRole("button", { name: "Adicionar certificado" }));
    fireEvent.change(within(dialog).getByRole("textbox", { name: /^Nome/ }), { target: { value: "loja" } });
    const pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----\n";
    fireEvent.change(within(dialog).getByRole("textbox", { name: /PEM/ }), { target: { value: pem } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Guardar certificado" }));

    await waitFor(() => expect(within(dialog).getByText("CA Loja")).toBeInTheDocument());
    expect(sentBody("POST", "/api/certificates")).toEqual({ name: "loja", pem });
    expect(within(dialog).queryByRole("textbox", { name: /PEM/ })).not.toBeInTheDocument();
    expect(dialog.textContent).not.toMatch(/PRIVATE KEY/);
    expect(within(dialog).getByText("12:34")).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(sentBody("POST", "/api/domains").tls).toEqual({ mode: "custom", certId: 7 });
  });

  it("erros do servidor aparecem no campo certo (INVALID_ROOT, CERT_MISMATCH)", async () => {
    let next = fail("INVALID_ROOT", "Pasta inválida. Use um caminho absoluto dentro de /var/www.", "routes[0].root");
    routes["POST /api/domains"] = () => next;
    const dialog = await openNew();
    tab(dialog, "Pasta");
    fireEvent.change(within(dialog).getByRole("textbox", { name: /^Pasta/ }), { target: { value: "/var/www/../etc" } });

    // Vai para a aba Certificado antes de salvar; o erro traz de volta para Rotas.
    tab(dialog, "Certificado");
    fireEvent.click(within(dialog).getByRole("radio", { name: /Próprio/ }));
    const select = await within(dialog).findByRole("combobox", { name: /^Certificado/ });
    await within(select).findByRole("option", { name: /curinga/ });
    fireEvent.change(select, { target: { value: "4" } });

    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));
    const folder = await within(dialog).findByRole("textbox", { name: /^Pasta/ });
    await waitFor(() => expect(folder).toHaveAttribute("aria-invalid", "true"));
    expect(within(dialog).getByText(/Pasta inválida/)).toBeInTheDocument();
    expect(within(dialog).getByRole("tab", { name: "Rotas" })).toHaveAttribute("aria-selected", "true");

    next = fail("CERT_MISMATCH", "O certificado escolhido não cobre loja.exemplo.com.", "tls.certId");
    fireEvent.change(folder, { target: { value: "/var/www/loja" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));
    const cert = await within(dialog).findByRole("combobox", { name: /^Certificado/ });
    await waitFor(() => expect(cert).toHaveAttribute("aria-invalid", "true"));
    expect(within(dialog).getByText(/não cobre loja\.exemplo\.com/)).toBeInTheDocument();
    expect(within(dialog).getByRole("tab", { name: "Certificado" })).toHaveAttribute("aria-selected", "true");
  });

  it("lista mostra selos Certificado próprio, Só Cloudflare e aviso de vencimento", async () => {
    routes["GET /api/domains"] = () =>
      json({
        domains: [
          panelDomain,
          domain({
            id: 2,
            cloudflareOnly: true,
            tls: { mode: "custom", certId: 4, certName: "curinga", notAfter: "2026-11-30T00:00:00Z", expiresSoon: true },
          }),
          domain({ id: 3, hostname: "blog.exemplo.com" }),
        ],
      });
    routes["GET /api/certificates"] = () =>
      json({
        certificates: [{ ...curinga, usedBy: ["loja.exemplo.com"], notAfter: "2026-11-30T00:00:00Z", expiresSoon: true }],
      });
    routes["DELETE /api/certificates/4"] = () =>
      fail("CERT_IN_USE", "Este certificado está em uso por um domínio. Troque o certificado do domínio antes de apagar.");
    renderPage();
    await screen.findByRole("link", { name: /^painel\.exemplo\.com/ });

    const l = row("loja.exemplo.com");
    expect(within(l).getByText("Certificado próprio")).toBeInTheDocument();
    expect(within(l).getByText("Só Cloudflare")).toBeInTheDocument();
    expect(within(l).getByText("Vence em 30/11/2026")).toBeInTheDocument();
    const b = row("blog.exemplo.com");
    expect(within(b).queryByText("Certificado próprio")).not.toBeInTheDocument();
    expect(within(b).queryByText("Só Cloudflare")).not.toBeInTheDocument();

    // Seção Certificados: uso, validade e exclusão recusada quando em uso.
    const section = screen.getByRole("region", { name: "Certificados" });
    const certRow = (await within(section).findByText("curinga")).closest("tr") as HTMLElement;
    expect(within(certRow).getByText("loja.exemplo.com")).toBeInTheDocument();
    expect(within(certRow).getByText("30/11/2026")).toBeInTheDocument();
    expect(within(certRow).getByText("Vence em breve")).toBeInTheDocument();
    fireEvent.click(within(certRow).getByRole("button", { name: /Apagar/ }));
    const confirm = screen.getByRole("dialog", { name: /Apagar certificado/ });
    fireEvent.click(within(confirm).getByRole("button", { name: "Apagar" }));
    expect(await within(confirm).findByText(/está em uso por um domínio/)).toBeInTheDocument();
  });

  it("domínio simples continua com uma rota '/' e parece o formulário de antes", async () => {
    const simple = domain({ id: 2 });
    routes["GET /api/domains"] = () => json({ domains: [panelDomain, simple] });
    routes["PUT /api/domains/2"] = () => okSave(simple);
    renderPage();
    await screen.findByRole("link", { name: /^loja\.exemplo\.com/ });

    fireEvent.click(within(row("loja.exemplo.com")).getByRole("button", { name: /Editar/ }));
    const dialog = screen.getByRole("dialog", { name: "Editar domínio" });
    expect(within(dialog).getByRole("tab", { name: "Rotas" })).toHaveAttribute("aria-selected", "true");
    expect(within(dialog).getByRole("tab", { name: "Endereço" })).toHaveAttribute("aria-selected", "true");
    expect(within(dialog).getByRole("textbox", { name: /host:porta/ })).toHaveValue("127.0.0.1:5000");
    expect(within(dialog).getByRole("checkbox", { name: /Forçar HTTPS/ })).toBeChecked();
    expect(within(dialog).getByLabelText(/Redirecionamento www/)).toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: "Caminho" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("group", { name: /^Rota / })).not.toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    // O objeto completo vai no PUT: o que se vê é o que se grava.
    expect(sentBody("PUT", "/api/domains/2")).toEqual({
      hostname: "loja.exemplo.com",
      wwwRedirect: "none",
      forceHttps: true,
      routes: [root()],
      tls: { mode: "auto", certId: null },
      cloudflareOnly: false,
      headers: [],
      compression: true,
    });
  });

  it("domínio do painel edita só certificado e segurança, sem rotas; PANEL_PORT_OFF aparece", async () => {
    routes["PUT /api/domains/9"] = () =>
      fail("PANEL_PORT_OFF", "Ligue a porta pública do painel antes de ativar o Só Cloudflare.", undefined, 409);
    renderPage();
    await screen.findByRole("link", { name: /^painel\.exemplo\.com/ });

    const p = row("painel.exemplo.com");
    expect(within(p).queryByRole("button", { name: /Remover/ })).not.toBeInTheDocument();
    fireEvent.click(within(p).getByRole("button", { name: /Editar/ }));
    const dialog = screen.getByRole("dialog", { name: "Editar domínio do painel" });
    expect(within(dialog).queryByRole("tab", { name: "Rotas" })).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/Hostname/)).not.toBeInTheDocument();

    tab(dialog, "Segurança");
    fireEvent.click(within(dialog).getByRole("switch", { name: /Só Cloudflare/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Salvar" }));
    expect(await within(dialog).findByText(/Ligue a porta pública/)).toBeInTheDocument();
    expect(sentBody("PUT", "/api/domains/9")).toEqual({
      hostname: "painel.exemplo.com",
      tls: { mode: "auto", certId: null },
      cloudflareOnly: true,
      headers: [],
      compression: false,
    });
  });
});
