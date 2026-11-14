import { describe, expect, it } from "vitest";
import { auditDetail, type AuditItem } from "../audit";

const base: AuditItem = { id: 1, userId: 1, action: "x", target: "", ip: "", metadata: {}, createdAt: "" };

describe("auditDetail", () => {
  it("mostra o nome do token quando a requisição veio por token", () => {
    expect(auditDetail({ ...base, target: "app:3", metadata: { via: "token", token: "claude-code" } })).toBe(
      "token claude-code",
    );
  });
  it("mantém e-mail, sessão e traço", () => {
    expect(auditDetail({ ...base, metadata: { email: "a@b.c" } })).toBe("a@b.c");
    expect(auditDetail({ ...base, target: "abcdef123456" })).toBe("sessão abcdef12…");
    expect(auditDetail(base)).toBe("—");
  });
});
