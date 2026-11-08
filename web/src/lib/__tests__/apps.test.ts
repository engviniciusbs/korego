import { afterEach, describe, expect, it, vi } from "vitest";
import { defaultsFor } from "../apps";
import { submitManifestForm } from "../github";
import { ApiError, api } from "../api";

describe("defaultsFor", () => {
  it("compose exige serviço e mostra caminhos", () => {
    const d = defaultsFor("github", "compose");
    expect(d.required).toContain("composeService");
    expect(d.visible).toEqual(
      expect.arrayContaining(["repo", "branch", "composePath", "composeService"]),
    );
    // O Dockerfile só vale para o build dockerfile.
    expect(d.visible).not.toContain("dockerfilePath");
    expect(d.composePath).toBe("docker-compose.yml");
  });
  it("image esconde branch e caminhos", () => {
    const d = defaultsFor("image", "image");
    expect(d.visible).toContain("image");
    for (const f of ["repo", "branch", "dockerfilePath", "composePath", "composeService"]) {
      expect(d.visible).not.toContain(f);
    }
    expect(d.required).toEqual(expect.arrayContaining(["image", "internalPort"]));
  });
  it("dockerfile e railpack não pedem compose", () => {
    const d = defaultsFor("git", "dockerfile");
    expect(d.visible).not.toContain("composePath");
    expect(d.visible).toContain("dockerfilePath");
    expect(d.branch).toBe("main");
    const r = defaultsFor("git", "railpack");
    expect(r.visible).not.toContain("dockerfilePath");
  });
});

describe("submitManifestForm", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("monta o form, envia e remove", () => {
    let seen: { action: string; method: string; value: string } | null = null;
    vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(function (this: HTMLFormElement) {
      seen = {
        action: this.action,
        method: this.method,
        value: (this.querySelector("input[name=manifest]") as HTMLInputElement).value,
      };
      expect(document.body.contains(this)).toBe(true);
    });
    submitManifestForm("https://github.com/settings/apps/new?state=abc", '{"name":"x"}');
    expect(seen).toEqual({
      action: "https://github.com/settings/apps/new?state=abc",
      method: "post",
      value: '{"name":"x"}',
    });
    expect(document.querySelector("form")).toBeNull();
  });

  it("recusa action que não seja https", () => {
    const spy = vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(() => {});
    expect(() => submitManifestForm("http://evil.example/x", "{}")).toThrow();
    expect(() => submitManifestForm("javascript:alert(1)", "{}")).toThrow();
    expect(spy).not.toHaveBeenCalled();
    expect(document.querySelector("form")).toBeNull();
  });
});

describe("ApiError.field", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("expõe o campo do erro de validação", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({ error: { code: "INVALID_APP", message: "Slug ruim", field: "slug" } }),
          { status: 400 },
        ),
      ),
    );
    const err = (await api("/api/apps", { method: "POST", body: {} }).catch((x) => x)) as ApiError;
    expect(err.code).toBe("INVALID_APP");
    expect(err.field).toBe("slug");
  });

  it("field fica indefinido quando ausente ou vazio", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response(JSON.stringify({ error: { code: "X", message: "m" } }), { status: 400 }),
        ),
    );
    const err = (await api("/x").catch((x) => x)) as ApiError;
    expect(err.field).toBeUndefined();
  });
});
