import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AppView } from "@/lib/apps";
import type { Deploy } from "@/lib/deploys";
import { DeployList } from "../deploy-list";

const nav = vi.hoisted(() => ({ push: vi.fn() }));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: nav.push, replace: nav.push }),
  useSearchParams: () => new URLSearchParams(""),
  usePathname: () => "/",
}));

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

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const dep = (id: number, over: Partial<Deploy> = {}): Deploy => ({
  id,
  appId: 5,
  trigger: "manual",
  status: "superseded",
  reason: "",
  commitSha: `${id}abcdef0123456789`,
  commitMessage: `versão ${id}`,
  image: `korego/loja-web:${id}`,
  hostPort: 20000 + id,
  rollbackOf: null,
  createdAt: "2026-11-08T12:00:00Z",
  startedAt: "2026-11-08T12:00:00Z",
  finishedAt: "2026-11-08T12:01:30Z",
  ...over,
});

const app = (over: Partial<AppView> = {}) =>
  ({
    id: 5,
    projectId: 1,
    slug: "loja-web",
    sourceType: "github",
    repo: "exemplo/loja",
    image: "",
    buildType: "dockerfile",
    branch: "main",
    internalPort: 80,
    healthTimeoutS: 60,
    autoDeploy: true,
    hostPort: 20005,
    currentDeploy: { id: 5, status: "running", commitSha: "5abcdef", finishedAt: "2026-11-08T12:01:30Z" },
    domains: [],
    url: "http://127.0.0.1:20005",
    ...over,
  }) as AppView;

type Handler = (init?: { method?: string; body?: string }) => Response;
let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  nav.push.mockReset();
  routes = {
    "GET /api/apps/5/deploys": () =>
      json({
        deploys: [
          dep(6, { status: "building", finishedAt: null, trigger: "webhook" }),
          dep(5, { status: "running" }),
          dep(4),
          dep(3, { status: "failed", image: "" }),
          dep(2, { trigger: "rollback" }),
          dep(1),
        ],
      }),
  };
  vi.stubGlobal("WebSocket", FakeWS);
  fetchMock = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const h = routes[`${init?.method ?? "GET"} ${url.split("?")[0]}`];
    return h ? h(init) : json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderList(a: AppView) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <DeployList app={a} />
    </QueryClientProvider>,
  );
}

const row = (id: number) => screen.getByRole("row", { name: new RegExp(`Deploy #${id}\\b`) });
const rollbackIn = (id: number) => within(row(id)).queryByRole("button", { name: /Voltar para esta versão/ });

describe("DeployList", () => {
  it("DeployList esconde rollback para compose e no deploy atual", async () => {
    const { unmount } = renderList(app());
    await screen.findByRole("row", { name: /Deploy #6\b/ });

    expect(within(row(5)).getByText("No ar", { selector: "[data-current]" })).toBeInTheDocument();
    expect(within(row(6)).getByText("Push")).toBeInTheDocument();
    expect(within(row(2)).getByText("Rollback")).toBeInTheDocument();
    expect(within(row(4)).getByText("4abcdef")).toBeInTheDocument();
    expect(within(row(6)).getByText("em andamento")).toBeInTheDocument();
    expect(within(row(4)).getByText("1 min 30 s")).toBeInTheDocument();
    expect(within(row(4)).getByRole("link", { name: /Ver log/ })).toHaveAttribute("href", "/deploys/detalhe/?id=4");

    // Elegíveis: 5, 4, 2 (os 3 mais recentes com imagem); fora o atual (5).
    expect(rollbackIn(5)).toBeNull();
    expect(rollbackIn(4)).not.toBeNull();
    expect(rollbackIn(3)).toBeNull();
    expect(rollbackIn(2)).not.toBeNull();
    expect(rollbackIn(1)).toBeNull();
    expect(rollbackIn(6)).toBeNull();
    unmount();

    renderList(app({ buildType: "compose" }));
    await screen.findByRole("row", { name: /Deploy #6\b/ });
    expect(screen.queryByRole("button", { name: /Voltar para esta versão/ })).toBeNull();
  });

  it("cancelar pede confirmação e chama a API", async () => {
    routes["POST /api/deploys/6/cancel"] = () => new Response(null, { status: 204 });
    renderList(app());
    await screen.findByRole("row", { name: /Deploy #6\b/ });
    expect(within(row(5)).queryByRole("button", { name: /Cancelar/ })).toBeNull();

    fireEvent.click(within(row(6)).getByRole("button", { name: /Cancelar/ }));
    const dialog = await screen.findByRole("dialog", { name: "Cancelar o deploy #6?" });
    expect(fetchMock).not.toHaveBeenCalledWith("/api/deploys/6/cancel", expect.anything());
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar deploy" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/deploys/6/cancel", expect.objectContaining({ method: "POST" })),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("rollback confirma, chama a API e abre o log do novo deploy", async () => {
    routes["POST /api/apps/5/rollback"] = (init) => {
      expect(JSON.parse(init?.body ?? "{}")).toEqual({ deployId: 4 });
      return json({ deploy: dep(7, { status: "queued", trigger: "rollback" }) }, 202);
    };
    renderList(app());
    await screen.findByRole("row", { name: /Deploy #6\b/ });
    fireEvent.click(rollbackIn(4)!);
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Voltar para esta versão" }));
    await waitFor(() => expect(nav.push).toHaveBeenCalledWith("/deploys/detalhe/?id=7"));
  });

  it("rollback mostra o erro do servidor", async () => {
    routes["POST /api/apps/5/rollback"] = () =>
      json({ error: { code: "IMAGE_GONE", message: "A imagem deste deploy não existe mais." } }, 409);
    renderList(app());
    await screen.findByRole("row", { name: /Deploy #6\b/ });
    fireEvent.click(rollbackIn(4)!);
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Voltar para esta versão" }));
    expect(await within(dialog).findByText("A imagem deste deploy não existe mais.")).toBeInTheDocument();
    expect(nav.push).not.toHaveBeenCalled();
  });
});
