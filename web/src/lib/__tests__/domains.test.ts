import { describe, expect, it } from "vitest";
import { certNotAfter } from "../domains";

describe("certNotAfter", () => {
  it("data zero do Go (ano 1) conta como ausente", () => {
    expect(certNotAfter({ state: "pending", notAfter: "0001-01-01T00:00:00Z" })).toBeNull();
  });

  it("qualquer ano antes de 1970, vazio ou inválido conta como ausente", () => {
    expect(certNotAfter({ state: "active", notAfter: "1969-12-31T23:59:59Z" })).toBeNull();
    expect(certNotAfter({ state: "active", notAfter: "" })).toBeNull();
    expect(certNotAfter({ state: "active" })).toBeNull();
    expect(certNotAfter({ state: "active", notAfter: "lixo" })).toBeNull();
  });

  it("data real vira Date", () => {
    const d = certNotAfter({ state: "active", notAfter: "2027-02-01T12:00:00Z" });
    expect(d?.toISOString()).toBe("2027-02-01T12:00:00.000Z");
  });
});
