import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import DetalhePage from "../(painel)/aplicacoes/detalhe/page";

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams("id=9"),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
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

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const staticApp = {
  id: 9,
  projectId: 1,
  slug: "site",
  sourceType: "git",
  repo: "https://github.com/exemplo/site.git",
  image: "",
  buildType: "static",
  branch: "main",
  dockerfilePath: "",
  composePath: "",
  composeService: "",
  internalPort: 0,
  healthTimeoutS: 0,
  autoDeploy: false,
  hostPort: 0,
  staticBuildCmd: "",
  staticOutputDir: "dist",
  staticBaseDir: ".",
  staticImage: "node:22-alpine",
  staticSpa: false,
  staticImmutablePrefix: "/assets/",
  publishRoot: "/var/www/site",
  currentDeploy: { id: 4, status: "running", commitSha: "abcdef0123", finishedAt: "2026-11-08T12:01:30Z" },
  domains: [],
  url: "",
  createdAt: "2026-11-08T12:00:00Z",
  updatedAt: "2026-11-08T12:00:00Z",
};

beforeEach(() => {
  vi.stubGlobal("WebSocket", FakeWS);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const path = url.split("?")[0];
      if (path === "/api/apps/9") return json({ app: staticApp });
      if (path === "/api/apps/9/deploys") {
        return json({
          deploys: [
            {
              id: 4,
              appId: 9,
              trigger: "manual",
              status: "running",
              reason: "",
              commitSha: "abcdef0123",
              commitMessage: "",
              image: "",
              hostPort: 0,
              rollbackOf: null,
              createdAt: "2026-11-08T12:00:00Z",
              startedAt: "2026-11-08T12:00:00Z",
              finishedAt: "2026-11-08T12:01:30Z",
              releaseDir: "/var/www/site/releases/20261108120000-abcdef0",
            },
          ],
        });
      }
      if (path === "/api/projects") return json({ projects: [] });
      return json({ error: { code: "NOT_FOUND", message: "não" } }, 404);
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Detalhe da aplicação estática", () => {
  it("mostra a raiz e a versão atual no lugar de porta e endereço", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <DetalhePage />
      </QueryClientProvider>,
    );
    expect(await screen.findByText("Raiz publicada")).toBeInTheDocument();
    expect(screen.getByText("/var/www/site")).toBeInTheDocument();
    expect(await screen.findByText("20261108120000-abcdef0")).toBeInTheDocument();
    expect(screen.queryByText("Porta interna")).not.toBeInTheDocument();
    expect(screen.queryByText("Endereço local")).not.toBeInTheDocument();
    expect(screen.queryByText("Verificação de saúde")).not.toBeInTheDocument();
  });
});
