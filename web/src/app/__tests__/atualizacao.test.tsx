import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import AtualizacaoPage from "../(painel)/configuracoes/atualizacao/page";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <AtualizacaoPage />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Atualização", () => {
  it("mostra o botão e pede confirmação quando há atualização", async () => {
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? json({ status: "updating" }, 202)
        : json({ current: "0.1.0", latest: "v0.2.0", available: true }),
    );
    vi.stubGlobal("fetch", fetchMock);
    renderPage();

    fireEvent.click(await screen.findByRole("button", { name: "Atualizar para v0.2.0" }));
    expect(screen.getByText(/ficará indisponível/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Confirmar atualização" }));

    expect(await screen.findByText("Atualizando… o painel vai reconectar.")).toBeInTheDocument();
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/update", expect.objectContaining({ method: "POST" })),
    );
  });

  it("informa quando já está na versão mais recente", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => json({ current: "0.2.0", latest: "v0.2.0", available: false })));
    renderPage();
    expect(await screen.findByText("O KoreGO já está na versão mais recente.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Atualizar para/ })).toBeNull();
  });

  it("mostra o erro da verificação sem botão", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => json({ current: "0.1.0", latest: null, available: false, error: "sem rede" })),
    );
    renderPage();
    expect(await screen.findByText("sem rede")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Atualizar para/ })).toBeNull();
  });

  it("avisa que builds dev não se atualizam", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => json({ current: "dev", latest: "v0.2.0", available: false })));
    renderPage();
    expect(await screen.findByText(/não se atualizam automaticamente/)).toBeInTheDocument();
  });
});
