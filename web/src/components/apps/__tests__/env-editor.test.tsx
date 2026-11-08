import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EnvEditor } from "../env-editor";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

let names: string[];
let putBodies: unknown[];
let putResponse: () => Response;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  names = ["DB_URL", "SECRET_KEY"];
  putBodies = [];
  putResponse = () => new Response(null, { status: 204 });
  fetchMock = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    if (url === "/api/apps/5/env" && (init?.method ?? "GET") === "GET") return json({ names });
    if (url === "/api/apps/5/env" && init?.method === "PUT") {
      putBodies.push(JSON.parse(init.body ?? "{}"));
      return putResponse();
    }
    return json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderEditor() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <EnvEditor appId={5} />
    </QueryClientProvider>,
  );
}

const rowOf = (name: string) => screen.getByText(name, { selector: "td *, td" }).closest("tr") as HTMLElement;

describe("EnvEditor", () => {
  it("EnvEditor nunca mostra valores e envia só as mudanças", async () => {
    const { container } = renderEditor();
    await screen.findByText("DB_URL");

    // Valores sempre mascarados; nenhum campo vem preenchido.
    expect(within(rowOf("DB_URL")).getByText("••••••")).toBeInTheDocument();
    expect(within(rowOf("SECRET_KEY")).getByText("••••••")).toBeInTheDocument();
    expect(container.querySelectorAll("input")).toHaveLength(0);
    expect(screen.queryByText("Alterações não salvas")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Salvar variáveis" })).toBeDisabled();

    // Substituir valor: campo de senha vazio, sem autocomplete.
    fireEvent.click(within(rowOf("DB_URL")).getByRole("button", { name: /Substituir valor/ }));
    const replace = screen.getByLabelText("Novo valor de DB_URL");
    expect(replace).toHaveAttribute("type", "password");
    expect(replace).toHaveAttribute("autocomplete", "off");
    expect(replace).toHaveValue("");
    fireEvent.change(replace, { target: { value: "postgres://novo" } });

    // Remover e desfazer; depois remover de vez.
    fireEvent.click(within(rowOf("SECRET_KEY")).getByRole("button", { name: /Remover/ }));
    fireEvent.click(within(rowOf("SECRET_KEY")).getByRole("button", { name: /Desfazer/ }));
    fireEvent.click(within(rowOf("SECRET_KEY")).getByRole("button", { name: /Remover/ }));

    // Adicionar: nome inválido é recusado no cliente.
    fireEvent.click(screen.getByRole("button", { name: "Adicionar variável" }));
    const name = screen.getByLabelText("Nome da nova variável 1");
    const value = screen.getByLabelText("Valor da nova variável 1");
    expect(value).toHaveAttribute("type", "password");
    expect(value).toHaveAttribute("autocomplete", "off");
    fireEvent.change(name, { target: { value: "1-ruim" } });
    fireEvent.change(value, { target: { value: "x" } });
    expect(screen.getByText("Alterações não salvas")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Salvar variáveis" }));
    expect(await screen.findByText(/Nome inválido/)).toBeInTheDocument();
    expect(putBodies).toHaveLength(0);

    fireEvent.change(name, { target: { value: "PORT" } });
    // O servidor passa a ter estes nomes; a lista é buscada de novo após salvar.
    names = ["DB_URL", "PORT"];
    fireEvent.click(screen.getByRole("button", { name: "Salvar variáveis" }));
    await waitFor(() => expect(putBodies).toHaveLength(1));
    expect(putBodies[0]).toEqual({
      set: { DB_URL: "postgres://novo", PORT: "x" },
      remove: ["SECRET_KEY"],
    });

    // Depois de salvar, a lista volta só com nomes mascarados.
    await waitFor(() => expect(screen.queryByText("Alterações não salvas")).not.toBeInTheDocument());
    await screen.findByText("PORT");
    expect(container.querySelectorAll("input")).toHaveLength(0);
    expect(screen.queryByText("postgres://novo")).not.toBeInTheDocument();
  });

  it("substituição vazia é ignorada e não apaga o valor", async () => {
    renderEditor();
    await screen.findByText("DB_URL");

    // Só abrir a substituição não conta como alteração e não envia nada.
    fireEvent.click(within(rowOf("DB_URL")).getByRole("button", { name: /Substituir valor/ }));
    expect(screen.getByText("Digite o novo valor ou cancele.")).toBeInTheDocument();
    expect(screen.queryByText("Alterações não salvas")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Salvar variáveis" })).toBeDisabled();

    // Com outra mudança, o nome com substituição vazia fica fora do pedido.
    fireEvent.click(within(rowOf("SECRET_KEY")).getByRole("button", { name: /Substituir valor/ }));
    fireEvent.change(screen.getByLabelText("Novo valor de SECRET_KEY"), { target: { value: "novo" } });
    // Variável nova pode ter valor vazio.
    fireEvent.click(screen.getByRole("button", { name: "Adicionar variável" }));
    fireEvent.change(screen.getByLabelText("Nome da nova variável 1"), { target: { value: "VAZIA" } });
    expect(screen.getByText("Alterações não salvas")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Salvar variáveis" }));
    await waitFor(() => expect(putBodies).toHaveLength(1));
    expect(putBodies[0]).toEqual({ set: { SECRET_KEY: "novo", VAZIA: "" }, remove: [] });
  });

  it("nome repetido e erro do servidor aparecem na variável", async () => {
    putResponse = () =>
      json({ error: { code: "INVALID_ENV", message: "Nome de variável inválido: API_KEY", field: "API_KEY" } }, 400);
    renderEditor();
    await screen.findByText("DB_URL");

    fireEvent.click(screen.getByRole("button", { name: "Adicionar variável" }));
    fireEvent.change(screen.getByLabelText("Nome da nova variável 1"), { target: { value: "DB_URL" } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar variáveis" }));
    expect(await screen.findByText(/já existe/i)).toBeInTheDocument();
    expect(putBodies).toHaveLength(0);

    fireEvent.change(screen.getByLabelText("Nome da nova variável 1"), { target: { value: "API_KEY" } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar variáveis" }));
    expect(await screen.findByText("Nome de variável inválido: API_KEY")).toBeInTheDocument();
    expect(screen.getByLabelText("Nome da nova variável 1")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("Alterações não salvas")).toBeInTheDocument();
  });
});
