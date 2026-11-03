import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, api, setCsrfToken } from "../api";

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  setCsrfToken("");
});

describe("api", () => {
  it("lança ApiError com code e status", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          json(403, { error: { code: "SETUP_TOKEN_REQUIRED", message: "Token exigido" } }),
        ),
    );
    const err = (await api("/api/setup", { method: "POST", body: {} }).catch((e) => e)) as ApiError;
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe("SETUP_TOKEN_REQUIRED");
    expect(err.status).toBe(403);
    expect(err.message).toBe("Token exigido");
  });

  it("envia X-CSRF-Token só em métodos que alteram estado", async () => {
    const fetchMock = vi.fn().mockImplementation(async () => json(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    setCsrfToken("tok123");
    await api("/api/x");
    await api("/api/x", { method: "POST", body: { a: 1 } });
    const get = fetchMock.mock.calls[0][1];
    const post = fetchMock.mock.calls[1][1];
    expect(new Headers(get.headers).has("X-CSRF-Token")).toBe(false);
    expect(new Headers(get.headers).has("Content-Type")).toBe(false);
    expect(new Headers(post.headers).get("X-CSRF-Token")).toBe("tok123");
    expect(new Headers(post.headers).get("Content-Type")).toBe("application/json");
    expect(post.body).toBe('{"a":1}');
    expect(post.credentials).toBe("same-origin");
  });

  it("dispara korego:unauthorized em 401", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(json(401, { error: { code: "UNAUTHORIZED", message: "x" } })),
    );
    const handler = vi.fn();
    window.addEventListener("korego:unauthorized", handler);
    await expect(api("/api/auth/me")).rejects.toBeInstanceOf(ApiError);
    window.removeEventListener("korego:unauthorized", handler);
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it("204 resolve undefined; erro não-JSON vira HTTP_<status>; rede vira NETWORK_ERROR", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    expect(await api("/api/auth/logout", { method: "POST" })).toBeUndefined();
    fetchMock.mockResolvedValueOnce(new Response("<html>", { status: 502 }));
    await expect(api("/api/x")).rejects.toMatchObject({ code: "HTTP_502", status: 502 });
    fetchMock.mockRejectedValueOnce(new TypeError("failed"));
    await expect(api("/api/x")).rejects.toMatchObject({ code: "NETWORK_ERROR", status: 0 });
  });
});
