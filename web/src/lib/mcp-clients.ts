/**
 * Como conectar cada cliente de agente ao servidor MCP do KoreGO.
 * Tudo é montado no navegador a partir da origem do painel (e, no fluxo
 * manual, do token recém-criado); nada disso vai para o servidor.
 */

export type McpClientId = "cursor" | "vscode" | "claude-code" | "codex" | "gemini" | "windsurf";

/** A ação principal de um cartão: abrir o cliente, copiar um comando ou copiar a configuração. */
export type AgentAction =
  | { type: "link"; href: string; label: string }
  | { type: "command"; text: string }
  | { type: "config"; text: string; label: string };

export interface AgentClient {
  id: McpClientId;
  name: string;
  kind: "Editor" | "Terminal";
  action: AgentAction;
  /** O que acontece depois da ação, em uma frase. */
  note: string;
  /** Alternativa recolhida no cartão. */
  manual?: { label: string; text: string };
}

const NAME = "korego";
const COPY_CONFIG = "Copiar configuração";

function b64(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

const json = (v: unknown) => JSON.stringify(v, null, 2);

const cursorLink = (server: object) =>
  `cursor://anysphere.cursor-deeplink/mcp/install?name=${NAME}&config=${encodeURIComponent(b64(JSON.stringify(server)))}`;

const vscodeLink = (server: object) => `vscode:mcp/install?${encodeURIComponent(JSON.stringify(server))}`;

/** Linha para registrar o servidor MCP no Claude Code com um token manual. */
export function mcpAddCommand(origin: string, token: string): string {
  return `claude mcp add --transport http -s user ${NAME} ${origin}/mcp --header "Authorization: Bearer ${token}"`;
}

/** Fluxo OAuth: só a URL; o cliente abre o navegador para o usuário autorizar com o 2FA. */
export function oauthAgentClients(origin: string): AgentClient[] {
  const url = `${origin}/mcp`;
  const cursorServer = { url };
  const vscodeServer = { name: NAME, type: "http", url };
  return [
    {
      id: "cursor",
      name: "Cursor",
      kind: "Editor",
      action: { type: "link", href: cursorLink(cursorServer), label: "Adicionar ao Cursor" },
      note: "Abre o Cursor com o KoreGO configurado. Ele mesmo abre o navegador para você autorizar.",
      manual: { label: "~/.cursor/mcp.json", text: json({ mcpServers: { [NAME]: cursorServer } }) },
    },
    {
      id: "vscode",
      name: "VS Code",
      kind: "Editor",
      action: { type: "link", href: vscodeLink(vscodeServer), label: "Adicionar ao VS Code" },
      note: "Abre o VS Code com o KoreGO configurado. Ele mesmo abre o navegador para você autorizar.",
      manual: { label: "Terminal", text: `code --add-mcp '${JSON.stringify(vscodeServer)}'` },
    },
    {
      id: "claude-code",
      name: "Claude Code",
      kind: "Terminal",
      action: { type: "command", text: `claude mcp add --transport http -s user ${NAME} ${url}` },
      note: "Rode no terminal. Depois, em /mcp → korego → Authenticate, o navegador abre para autorizar.",
    },
    {
      id: "codex",
      name: "Codex",
      kind: "Terminal",
      action: { type: "command", text: `codex mcp add ${NAME} --url ${url}\ncodex mcp login ${NAME}` },
      note: "O segundo comando abre o navegador para autorizar.",
    },
    {
      id: "gemini",
      name: "Gemini CLI",
      kind: "Terminal",
      action: { type: "command", text: `gemini mcp add --scope user --transport http ${NAME} ${url}` },
      note: "Na primeira chamada o Gemini abre o navegador para autorizar (ou use /mcp auth korego).",
    },
    {
      id: "windsurf",
      name: "Windsurf",
      kind: "Editor",
      action: { type: "config", label: COPY_CONFIG, text: json({ mcpServers: { [NAME]: { serverUrl: url } } }) },
      note: "Cole em ~/.codeium/windsurf/mcp_config.json. Ainda não verificado com OAuth.",
    },
  ];
}

/** Fluxo manual: cada cliente recebe o token no cabeçalho Authorization. */
export function tokenAgentClients(origin: string, token: string): AgentClient[] {
  const url = `${origin}/mcp`;
  const auth = `Bearer ${token}`;
  const headers = { Authorization: auth };
  const cursorServer = { url, headers };
  const vscodeServer = { name: NAME, type: "http", url, headers };
  return [
    {
      id: "cursor",
      name: "Cursor",
      kind: "Editor",
      action: { type: "link", href: cursorLink(cursorServer), label: "Adicionar ao Cursor" },
      note: "Abre o Cursor com o KoreGO e este token configurados.",
      manual: { label: "~/.cursor/mcp.json", text: json({ mcpServers: { [NAME]: cursorServer } }) },
    },
    {
      id: "vscode",
      name: "VS Code",
      kind: "Editor",
      action: { type: "link", href: vscodeLink(vscodeServer), label: "Adicionar ao VS Code" },
      note: "Abre o VS Code com o KoreGO e este token configurados.",
      manual: { label: "Terminal", text: `code --add-mcp '${JSON.stringify(vscodeServer)}'` },
    },
    {
      id: "claude-code",
      name: "Claude Code",
      kind: "Terminal",
      action: { type: "command", text: mcpAddCommand(origin, token) },
      note: "Rode no terminal. Vale para todos os projetos.",
    },
    {
      id: "codex",
      name: "Codex",
      kind: "Terminal",
      action: {
        type: "config",
        label: COPY_CONFIG,
        text: `[mcp_servers.${NAME}]\nurl = "${url}"\nhttp_headers = { "Authorization" = "${auth}" }\n`,
      },
      note: "Cole em ~/.codex/config.toml.",
    },
    {
      id: "gemini",
      name: "Gemini CLI",
      kind: "Terminal",
      action: { type: "config", label: COPY_CONFIG, text: json({ mcpServers: { [NAME]: { httpUrl: url, headers } } }) },
      note: "Cole em ~/.gemini/settings.json.",
    },
    {
      id: "windsurf",
      name: "Windsurf",
      kind: "Editor",
      action: { type: "config", label: COPY_CONFIG, text: json({ mcpServers: { [NAME]: { serverUrl: url, headers } } }) },
      note: "Cole em ~/.codeium/windsurf/mcp_config.json.",
    },
  ];
}
