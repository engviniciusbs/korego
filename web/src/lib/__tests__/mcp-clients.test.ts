import { describe, expect, it } from "vitest";
import { mcpAddCommand, oauthAgentClients, tokenAgentClients, type AgentClient } from "../mcp-clients";

const O = "https://painel.exemplo.com";
const U = `${O}/mcp`;
const T = "kg_abc-_123";

const byId = (list: AgentClient[]) => Object.fromEntries(list.map((c) => [c.id, c]));

/** Todo texto de um cliente: ação e configuração manual. */
function texts(c: AgentClient): string[] {
  const a = c.action;
  const out = [a.type === "link" ? a.href : a.text];
  if (c.manual) out.push(c.manual.text);
  return out;
}

function cursorConfig(href: string): unknown {
  const p = new URL(href).searchParams;
  expect(p.get("name")).toBe("korego");
  return JSON.parse(atob(p.get("config")!));
}

function vscodeConfig(href: string): unknown {
  expect(href.startsWith("vscode:mcp/install?")).toBe(true);
  return JSON.parse(decodeURIComponent(href.slice("vscode:mcp/install?".length)));
}

describe("oauthAgentClients", () => {
  const list = oauthAgentClients(O);
  const c = byId(list);

  it("lista os seis clientes na ordem dos cartões", () => {
    expect(list.map((x) => x.id)).toEqual(["cursor", "vscode", "claude-code", "codex", "gemini", "windsurf"]);
    expect(list.map((x) => x.kind)).toEqual(["Editor", "Editor", "Terminal", "Terminal", "Terminal", "Editor"]);
  });

  it("nenhum texto ou link leva token", () => {
    for (const x of list) for (const t of texts(x)) expect(t).not.toMatch(/Authorization|Bearer/);
  });

  it("Cursor e VS Code instalam por link só com a URL", () => {
    expect(c.cursor.action.type).toBe("link");
    if (c.cursor.action.type !== "link") return;
    expect(c.cursor.action.label).toBe("Adicionar ao Cursor");
    expect(cursorConfig(c.cursor.action.href)).toEqual({ url: U });
    expect(JSON.parse(c.cursor.manual!.text)).toEqual({ mcpServers: { korego: { url: U } } });

    expect(c.vscode.action.type).toBe("link");
    if (c.vscode.action.type !== "link") return;
    expect(c.vscode.action.label).toBe("Adicionar ao VS Code");
    expect(vscodeConfig(c.vscode.action.href)).toEqual({ name: "korego", type: "http", url: U });
    expect(c.vscode.manual!.text).toBe(`code --add-mcp '{"name":"korego","type":"http","url":"${U}"}'`);
  });

  it("os CLIs recebem o comando exato", () => {
    expect(c["claude-code"].action).toEqual({ type: "command", text: `claude mcp add --transport http -s user korego ${U}` });
    expect(c.codex.action).toEqual({ type: "command", text: `codex mcp add korego --url ${U}\ncodex mcp login korego` });
    expect(c.gemini.action).toEqual({ type: "command", text: `gemini mcp add --scope user --transport http korego ${U}` });
  });

  it("Windsurf copia a configuração com serverUrl", () => {
    expect(c.windsurf.action.type).toBe("config");
    if (c.windsurf.action.type !== "config") return;
    expect(c.windsurf.action.label).toBe("Copiar configuração");
    expect(JSON.parse(c.windsurf.action.text)).toEqual({ mcpServers: { korego: { serverUrl: U } } });
  });
});

describe("tokenAgentClients", () => {
  const list = tokenAgentClients(O, T);
  const c = byId(list);
  const h = { Authorization: `Bearer ${T}` };

  it("o token está em toda ação e configuração manual", () => {
    expect(list).toHaveLength(6);
    for (const x of list) {
      const a = x.action;
      const all = a.type === "link" ? decodeURIComponent(a.href) + atobConfig(a.href) : a.text;
      expect(all).toContain(T);
      if (x.manual) expect(x.manual.text).toContain(T);
    }
  });

  it("Cursor e VS Code recebem os cabeçalhos no link", () => {
    if (c.cursor.action.type !== "link" || c.vscode.action.type !== "link") throw new Error("esperava link");
    expect(cursorConfig(c.cursor.action.href)).toEqual({ url: U, headers: h });
    expect(vscodeConfig(c.vscode.action.href)).toEqual({ name: "korego", type: "http", url: U, headers: h });
  });

  it("Claude Code usa mcpAddCommand; Codex e Gemini copiam a configuração", () => {
    expect(c["claude-code"].action).toEqual({ type: "command", text: mcpAddCommand(O, T) });
    expect(mcpAddCommand(O, T)).toBe(
      `claude mcp add --transport http -s user korego ${U} --header "Authorization: Bearer ${T}"`,
    );
    expect(c.codex.action).toEqual({
      type: "config",
      label: "Copiar configuração",
      text: `[mcp_servers.korego]\nurl = "${U}"\nhttp_headers = { "Authorization" = "Bearer ${T}" }\n`,
    });
    if (c.gemini.action.type !== "config" || c.windsurf.action.type !== "config") throw new Error("esperava config");
    expect(JSON.parse(c.gemini.action.text)).toEqual({ mcpServers: { korego: { httpUrl: U, headers: h } } });
    expect(JSON.parse(c.windsurf.action.text)).toEqual({ mcpServers: { korego: { serverUrl: U, headers: h } } });
  });
});

/** Config base64 do link do Cursor (vazia para outros links). */
function atobConfig(href: string): string {
  const cfg = href.startsWith("cursor:") ? new URL(href).searchParams.get("config") : null;
  return cfg ? atob(cfg) : "";
}

describe("link do Cursor", () => {
  it("escapa o base64 mesmo quando ele tem + / =", () => {
    // Este token gera base64 com "+", "/" e "=" na configuração.
    const tok = "kg_>>>???~~";
    const c = byId(tokenAgentClients(O, tok)).cursor;
    if (c.action.type !== "link") throw new Error("esperava link");
    const raw = c.action.href.split("config=")[1];
    expect(raw).not.toMatch(/[+/=]/);
    expect(cursorConfig(c.action.href)).toEqual({ url: U, headers: { Authorization: `Bearer ${tok}` } });
  });
});

