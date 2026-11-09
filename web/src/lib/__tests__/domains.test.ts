import { describe, expect, it } from "vitest";
import { certNotAfter, trialResult } from "../domains";

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

describe("trialResult", () => {
  const base = {
    host: "exemplo.com",
    auto: true,
    tlsOk: true,
    leafOk: false,
    paths: null,
    leaf: { names: null },
  };
  it("falha manda: !ok é failed mesmo com pending", () => {
    expect(trialResult({ ...base, ok: false, pending: true })).toBe("failed");
    expect(trialResult({ ...base, ok: false, pending: false })).toBe("failed");
  });
  it("ok com certificado a emitir é pending", () => {
    expect(trialResult({ ...base, ok: true, pending: true })).toBe("pending");
  });
  it("ok sem pendência é ok", () => {
    expect(trialResult({ ...base, ok: true, pending: false, leafOk: true })).toBe("ok");
  });
});
