import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider, useAuth } from "../auth-context";

function Probe() {
  const { status, user } = useAuth();
  return (
    <div>
      {status}:{user?.email ?? "-"}
    </div>
  );
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

function mockFetch(setup: boolean, me: Response) {
  const f = vi.fn(async (url: string) =>
    url === "/api/setup/status" ? json(200, { needsSetup: setup }) : me,
  );
  vi.stubGlobal("fetch", f);
  return f;
}

afterEach(() => vi.unstubAllGlobals());

describe("AuthProvider", () => {
  it("resolve para setup sem consultar /me", async () => {
    const f = mockFetch(true, json(401, {}));
    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );
    await screen.findByText("setup:-");
    expect(f.mock.calls.map((c) => c[0])).toEqual(["/api/setup/status"]);
  });

  it("resolve para anonymous em 401", async () => {
    mockFetch(false, json(401, { error: { code: "UNAUTHORIZED", message: "x" } }));
    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );
    await screen.findByText("anonymous:-");
  });

  it("resolve para authenticated e volta a anonymous em korego:unauthorized", async () => {
    mockFetch(
      false,
      json(200, {
        user: { id: "1", email: "a@exemplo.com", role: "admin", createdAt: "x" },
        csrfToken: "t",
      }),
    );
    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );
    await screen.findByText("authenticated:a@exemplo.com");
    window.dispatchEvent(new Event("korego:unauthorized"));
    await waitFor(() => expect(screen.getByText("anonymous:-")).toBeInTheDocument());
  });
});
