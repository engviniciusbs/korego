import { describe, expect, it } from "vitest";
import type { Container } from "../containers";
import type { Domain, DomainRoute } from "../domains";
import { detectSites, rootVersion, siteHref } from "../sites";

function route(r: Partial<DomainRoute>): DomainRoute {
  return { path: "/", targetType: "address", maxBodyBytes: 0, noBuffering: false, spa: false, ...r };
}

function domain(id: number, hostname: string, routes: DomainRoute[], extra: Partial<Domain> = {}): Domain {
  return {
    id, hostname, targetType: routes.find((r) => r.path === "/")?.targetType ?? "address",
    appId: null, appSlug: "", address: "", wwwRedirect: "none", forceHttps: true, isPanel: false, routes,
    tls: { mode: "auto", certId: null, expiresSoon: false }, cloudflareOnly: false, headers: [], compression: false,
    cert: { state: "active" } as Domain["cert"], createdAt: "", updatedAt: "", ...extra,
  };
}

function container(name: string, project: string, ports: Array<[string, number]> = [], extra: Partial<Container> = {}): Container {
  return {
    id: name, name, image: "img", state: "running", status: "Up", health: "", createdAt: "", project,
    service: name.split("-")[1] ?? "", managed: false,
    ports: ports.map(([ip, publicPort]) => ({ ip, privatePort: 80, publicPort, proto: "tcp" })), ...extra,
  };
}

const hoprsRoot = {
  path: "/", root: "/var/www/hoprs/current", resolved: "/var/www/hoprs/releases/20261115133041-9faa1c9",
  exists: true, modifiedAt: "2026-11-15T13:30:41Z",
};

// Dados iguais aos da VPS.
const domains: Domain[] = [
  domain(1, "korego.viniciusbsarmento.com", [route({ address: "127.0.0.1:8000" })], { isPanel: true }),
  domain(2, "viniciusbsarmento.com", [route({ path: "/api/", address: "127.0.0.1:8092" }), route({ address: "127.0.0.1:8091" })]),
  domain(4, "mikos.com.br", [route({ address: "127.0.0.1:8081" })]),
  domain(5, "www.mikos.com.br", [route({ address: "127.0.0.1:8081" })]),
  domain(6, "hoprs.online", [route({ targetType: "static", root: "/var/www/hoprs/current" })], { staticRoots: [hoprsRoot] }),
  domain(7, "solto.com", [route({ address: "127.0.0.1:9999" })]),
  domain(8, "app.com", [route({ targetType: "app", appId: 3 })]),
];
const containers: Container[] = [
  container("korego-caddy", "", [], { managed: true }),
  container("mikos-api-1", "mikos", [["", 0]]),
  container("mikos-caddy-1", "mikos", [["127.0.0.1", 8081]]),
  container("viniciusbsarmento-api-1", "viniciusbsarmento", [["127.0.0.1", 8092]]),
  container("viniciusbsarmento-web-1", "viniciusbsarmento", [["127.0.0.1", 8091]]),
  container("redis", ""),
];

describe("detectSites", () => {
  const sites = detectSites(domains, containers);
  const by = (name: string) => sites.find((s) => s.name === name);

  it("agrupa as stacks compose e liga os domínios pela porta", () => {
    const mikos = by("mikos")!;
    expect(mikos.kind).toBe("compose");
    expect(mikos.containers.map((c) => c.name)).toEqual(["mikos-api-1", "mikos-caddy-1"]);
    expect(mikos.domains.map((d) => d.hostname)).toEqual(["mikos.com.br", "www.mikos.com.br"]);
  });

  it("domínio com várias rotas aparece uma vez só", () => {
    const vbs = by("viniciusbsarmento")!;
    expect(vbs.domains.map((d) => d.hostname)).toEqual(["viniciusbsarmento.com"]);
    expect(sites.filter((s) => s.domains.some((d) => d.hostname === "viniciusbsarmento.com"))).toHaveLength(1);
  });

  it("site estático vira cartão próprio com a pasta", () => {
    const h = by("hoprs.online")!;
    expect(h.kind).toBe("estatico");
    expect(h.roots).toEqual([hoprsRoot]);
    expect(rootVersion(h.roots[0])).toBe("20261115133041-9faa1c9");
  });

  it("endereço sem container vira cartão Endereço; container sem compose vira avulso", () => {
    expect(by("solto.com")?.kind).toBe("endereco");
    expect(by("redis")?.kind).toBe("avulso");
  });

  it("deixa de fora o painel, os containers do KoreGO e domínios de apps", () => {
    const names = sites.map((s) => s.name);
    expect(names).not.toContain("korego.viniciusbsarmento.com");
    expect(names).not.toContain("korego-caddy");
    expect(names).not.toContain("app.com");
  });

  it("pasta inexistente não tem versão", () => {
    expect(rootVersion({ ...hoprsRoot, exists: false, resolved: "", modifiedAt: null })).toBe("");
  });

  it("siteHref codifica o nome", () => {
    expect(siteHref({ kind: "estatico", name: "a b.com" })).toBe("/projetos/site/?tipo=estatico&nome=a%20b.com");
  });
});
