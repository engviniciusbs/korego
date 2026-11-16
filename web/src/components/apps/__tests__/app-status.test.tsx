import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AppView } from "@/lib/apps";
import type { Container } from "@/lib/containers";
import { AppStatus, stackState } from "../page-bits";

const stackApp = {
  id: 20, projectId: 1, slug: "mikos", sourceType: "local", repo: "", image: "", buildType: "compose-local",
  branch: "", dockerfilePath: "", composePath: "", composeService: "", internalPort: 0, healthTimeoutS: 60,
  autoDeploy: false, hostPort: 0, currentDeploy: null, domains: [], url: "", staticBuildCmd: "", staticOutputDir: "",
  staticBaseDir: "", staticImage: "", staticSpa: false, staticImmutablePrefix: "", publishRoot: "",
  composeProject: "mikos", updateMode: "pull", updateScript: "", createdAt: "", updatedAt: "",
} as AppView;

const c = (name: string, project: string, state: string, managed = false): Container => ({
  id: name, name, image: "img", state, status: "", health: "", createdAt: "", project, service: "", managed, ports: [],
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderStatus(app: AppView, containers: Container[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ containers }), { status: 200, headers: { "Content-Type": "application/json" } })),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <AppStatus app={app} />
    </QueryClientProvider>,
  );
}

describe("stackState", () => {
  it("todos rodando, parte ou nenhum, só com os containers do projeto", () => {
    const outra = c("outra-web-1", "outra", "exited");
    const kore = c("korego-x", "mikos", "exited", true);
    expect(stackState([c("a", "mikos", "running"), c("b", "mikos", "running"), outra, kore], "mikos")).toBe("running");
    expect(stackState([c("a", "mikos", "running"), c("b", "mikos", "exited")], "mikos")).toBe("partial");
    expect(stackState([c("a", "mikos", "exited")], "mikos")).toBe("stopped");
    expect(stackState([outra], "mikos")).toBe("stopped");
  });
});

describe("AppStatus", () => {
  it("stack compose da VPS mostra o estado dos containers, não Sem deploy", async () => {
    renderStatus(stackApp, [c("a", "mikos", "running"), c("b", "mikos", "exited")]);
    expect(await screen.findByText("Parcial")).toBeInTheDocument();
    expect(screen.queryByText("Sem deploy")).not.toBeInTheDocument();
  });

  it("stack toda de pé fica Rodando", async () => {
    renderStatus(stackApp, [c("a", "mikos", "running")]);
    expect(await screen.findByText("Rodando")).toBeInTheDocument();
  });

  it("stack parada fica Parado", async () => {
    renderStatus(stackApp, [c("a", "mikos", "exited")]);
    expect(await screen.findByText("Parado")).toBeInTheDocument();
  });

  it("as demais apps seguem o deploy atual", () => {
    renderStatus({ ...stackApp, sourceType: "image", buildType: "image", composeProject: "" }, []);
    expect(screen.getByText("Sem deploy")).toBeInTheDocument();
  });
});
