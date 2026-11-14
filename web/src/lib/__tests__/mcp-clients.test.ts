import { describe, expect, it } from "vitest";
import { mcpAddCommand, mcpClients, mcpOAuthClients } from "../mcp-clients";

const O = "https://painel.exemplo.com";
const T = "kg_abc-_123";

describe("mcpClients", () => {
  const byId = Object.fromEntries(mcpClients(O, T).map((c) => [c.id, c]));

  it("Claude Code usa o comando claude mcp add", () => {
    expect(byId["claude-code"].snippet).toBe(mcpAddCommand(O, T));
    expect(mcpAddCommand(O, T)).toBe(
      `claude mcp add --transport http korego ${O}/mcp --header "Authorization: Bearer ${T}"`,
    );
  });

  it("os arquivos JSON são válidos e usam a chave de URL de cada cliente", () => {
    const h = { Authorization: `Bearer ${T}` };
    expect(JSON.parse(byId.cursor.snippet)).toEqual({ mcpServers: { korego: { url: `${O}/mcp`, headers: h } } });
    expect(JSON.parse(byId.gemini.snippet)).toEqual({ mcpServers: { korego: { httpUrl: `${O}/mcp`, headers: h } } });
    expect(JSON.parse(byId.windsurf.snippet)).toEqual({ mcpServers: { korego: { serverUrl: `${O}/mcp`, headers: h } } });
  });

  it("o link do VS Code carrega a configuração http", () => {
    const href = byId.vscode.installLink!.href;
    expect(href.startsWith("vscode:mcp/install?")).toBe(true);
    expect(JSON.parse(decodeURIComponent(href.slice("vscode:mcp/install?".length)))).toEqual({
      name: "korego",
      type: "http",
      url: `${O}/mcp`,
      headers: { Authorization: `Bearer ${T}` },
    });
  });

  it("Codex gera TOML com url e http_headers", () => {
    expect(byId.codex.snippet).toBe(
      `[mcp_servers.korego]\nurl = "${O}/mcp"\nhttp_headers = { "Authorization" = "Bearer ${T}" }\n`,
    );
  });
});

describe("mcpOAuthClients", () => {
  const P = "https://p.exemplo.com";
  const list = mcpOAuthClients(P);
  const byId = Object.fromEntries(list.map((c) => [c.id, c]));

  it("mantém os mesmos ids de mcpClients", () => {
    expect(list.map((c) => c.id)).toEqual(mcpClients(P, "x").map((c) => c.id));
  });

  it("nenhum trecho nem link carrega cabeçalho de autorização", () => {
    for (const c of list) {
      expect(c.snippet).not.toMatch(/Authorization|Bearer/);
      expect(c.installLink?.href ?? "").not.toMatch(/Authorization|Bearer/);
      if (c.installLink) expect(decodeURIComponent(c.installLink.href)).not.toMatch(/Authorization|Bearer/);
    }
  });

  it("Claude Code usa só a URL", () => {
    expect(byId["claude-code"].snippet).toBe("claude mcp add --transport http korego https://p.exemplo.com/mcp");
  });

  it("trechos de cada cliente", () => {
    const url = `${P}/mcp`;
    expect(JSON.parse(byId.cursor.snippet)).toEqual({ mcpServers: { korego: { url } } });
    expect(byId.cursor.hint).toBe("O Cursor ainda não autoriza por este fluxo; se falhar, use um token abaixo.");
    expect(byId.vscode.snippet).toBe(`code --add-mcp '${JSON.stringify({ name: "korego", type: "http", url })}'`);
    expect(byId.codex.snippet).toBe(`[mcp_servers.korego]\nurl = "${url}"\n`);
    expect(byId.codex.hint).toBe("Depois rode `codex mcp login korego`.");
    expect(JSON.parse(byId.gemini.snippet)).toEqual({ mcpServers: { korego: { httpUrl: url } } });
    expect(JSON.parse(byId.windsurf.snippet)).toEqual({ mcpServers: { korego: { serverUrl: url } } });
    expect(byId.outro.snippet).toBe(`URL: ${url}`);
  });
});
