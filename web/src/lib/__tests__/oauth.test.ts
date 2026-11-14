import { describe, expect, it } from "vitest";
import { safeNext } from "../oauth";

const id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";

describe("safeNext", () => {
  it("aceita o caminho de autorização com id de 43 caracteres", () => {
    expect(safeNext(`/autorizar/?req=${id}`)).toBe(`/autorizar/?req=${id}`);
  });
  it.each([
    "//evil.com",
    "/\\evil.com",
    "/\t/evil.com",
    "https://evil.com",
    "/autorizar/?req=abc",
    `/autorizar/?req=${id}&x=1`,
    `/autorizar/?req=${id}\n`,
    "",
    null,
  ])("recusa %j", (v) => {
    expect(safeNext(v)).toBeNull();
  });
});
