import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { oauthAgentClients } from "@/lib/mcp-clients";
import { AgentGrid } from "../agent-grid";

const O = "https://p.exemplo.com";
let writeText: ReturnType<typeof vi.fn>;

beforeEach(() => {
  writeText = vi.fn(() => Promise.resolve());
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
});
afterEach(() => vi.restoreAllMocks());

describe("AgentGrid", () => {
  it("mostra um cartão por cliente, com logo, nome e tipo", () => {
    render(<AgentGrid clients={oauthAgentClients(O)} />);
    const cards = screen.getAllByRole("article");
    expect(cards).toHaveLength(6);
    const cursor = screen.getByRole("article", { name: "Cursor" });
    expect(within(cursor).getByText("Editor")).toBeInTheDocument();
    expect(cursor.querySelector('[data-logo="cursor"] svg')).not.toBeNull();
    expect(within(screen.getByRole("article", { name: "Claude Code" })).getByText("Terminal")).toBeInTheDocument();
  });

  it("o link do editor aponta para a instalação", () => {
    render(<AgentGrid clients={oauthAgentClients(O)} />);
    const link = screen.getByRole("link", { name: "Adicionar ao VS Code" });
    expect(link.getAttribute("href")).toMatch(/^vscode:mcp\/install\?/);
  });

  it("Copiar copia o comando e mostra Copiado", async () => {
    render(<AgentGrid clients={oauthAgentClients(O)} />);
    fireEvent.click(screen.getByRole("button", { name: "Copiar comando do Claude Code" }));
    expect(writeText).toHaveBeenCalledWith(`claude mcp add --transport http -s user korego ${O}/mcp`);
    await waitFor(() => expect(screen.getByText("Copiado")).toBeInTheDocument());
  });

  it("Configuração manual só aparece quando o cliente tem uma", () => {
    render(<AgentGrid clients={oauthAgentClients(O)} />);
    expect(within(screen.getByRole("article", { name: "Cursor" })).getByText("Configuração manual")).toBeInTheDocument();
    expect(within(screen.getByRole("article", { name: "Codex" })).queryByText("Configuração manual")).toBeNull();
  });

  it("o botão de configuração copia o arquivo", () => {
    render(<AgentGrid clients={oauthAgentClients(O)} />);
    fireEvent.click(screen.getByRole("button", { name: "Copiar configuração do Windsurf" }));
    expect(JSON.parse(writeText.mock.calls[0][0])).toEqual({ mcpServers: { korego: { serverUrl: `${O}/mcp` } } });
  });
});
