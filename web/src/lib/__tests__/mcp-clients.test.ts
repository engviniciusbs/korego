import { describe, expect, it } from "vitest";
import { mcpAddCommand, mcpClients } from "../mcp-clients";

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
