/**
 * Como registrar o servidor MCP do KoreGO em cada cliente de agente.
 * Tudo é montado no navegador a partir da origem do painel e do token recém-criado;
 * nada disso vai para o servidor.
 */

export type McpClientId = "claude-code" | "cursor" | "vscode" | "codex" | "gemini" | "windsurf" | "outro";

export interface McpClientSetup {
  id: McpClientId;
  label: string;
  /** Onde colar o trecho, em uma frase. */
  hint: string;
  /** Trecho para copiar (comando ou arquivo de configuração). */
  snippet: string;
  /** Link que abre o cliente com a instalação pronta, quando ele oferece um. */
  installLink?: { href: string; label: string };
}

const NAME = "korego";

function b64(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

const json = (v: unknown) => JSON.stringify(v, null, 2);

/** Linha para registrar o servidor MCP no Claude Code. */
export function mcpAddCommand(origin: string, token: string): string {
  return `claude mcp add --transport http ${NAME} ${origin}/mcp --header "Authorization: Bearer ${token}"`;
}

export function mcpClients(origin: string, token: string): McpClientSetup[] {
  const url = `${origin}/mcp`;
  const auth = `Bearer ${token}`;
  const headers = { Authorization: auth };

  const cursorServer = { url, headers };
  const vscodeServer = { name: NAME, type: "http", url, headers };

  return [
    {
      id: "claude-code",
      label: "Claude Code",
      hint: "Rode no terminal, na pasta do projeto. Use -s user para valer em todos os projetos.",
      snippet: mcpAddCommand(origin, token),
    },
    {
      id: "cursor",
      label: "Cursor",
      hint: "Use o botão para instalar direto ou cole em ~/.cursor/mcp.json.",
      snippet: json({ mcpServers: { [NAME]: cursorServer } }),
      installLink: {
        href: `cursor://anysphere.cursor-deeplink/mcp/install?name=${NAME}&config=${encodeURIComponent(b64(JSON.stringify(cursorServer)))}`,
        label: "Abrir no Cursor",
      },
    },
    {
      id: "vscode",
      label: "VS Code",
      hint: "Use o botão para instalar direto ou rode o comando no terminal.",
      snippet: `code --add-mcp '${JSON.stringify(vscodeServer)}'`,
      installLink: {
        href: `vscode:mcp/install?${encodeURIComponent(JSON.stringify(vscodeServer))}`,
        label: "Abrir no VS Code",
      },
    },
    {
      id: "codex",
      label: "Codex",
      hint: "Adicione em ~/.codex/config.toml.",
      snippet: `[mcp_servers.${NAME}]\nurl = "${url}"\nhttp_headers = { "Authorization" = "${auth}" }\n`,
    },
    {
      id: "gemini",
      label: "Gemini CLI",
      hint: "Adicione em ~/.gemini/settings.json.",
      snippet: json({ mcpServers: { [NAME]: { httpUrl: url, headers } } }),
    },
    {
      id: "windsurf",
      label: "Windsurf",
      hint: "Adicione em ~/.codeium/windsurf/mcp_config.json.",
      snippet: json({ mcpServers: { [NAME]: { serverUrl: url, headers } } }),
    },
    {
      id: "outro",
      label: "Outro",
      hint: "Qualquer cliente MCP com transporte Streamable HTTP: informe a URL e o cabeçalho.",
      snippet: `URL: ${url}\nCabeçalho: Authorization: ${auth}`,
    },
  ];
}
