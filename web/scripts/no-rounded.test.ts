// @vitest-environment node
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const SRC = fileURLToPath(new URL("../src", import.meta.url));
const ROUNDED = /\brounded-(?!none\b)/;

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) return walk(full);
    return /\.(tsx?|css)$/.test(name) ? [full] : [];
  });
}

describe("sem cantos arredondados", () => {
  it("nenhum arquivo em src/ usa rounded-* além de rounded-none", () => {
    const files = walk(SRC);
    expect(files.length).toBeGreaterThan(0);
    const offenders = files
      .filter((f) => !f.includes("__tests__"))
      .flatMap((f) =>
        readFileSync(f, "utf8")
          .split("\n")
          .map((line, i) => ({ line, n: i + 1 }))
          .filter(({ line }) => ROUNDED.test(line))
          .map(({ n }) => `${relative(SRC, f)}:${n}`),
      );
    expect(offenders).toEqual([]);
  });

  it("todos os tokens --radius do tema valem 0", () => {
    const css = readFileSync(join(SRC, "app", "globals.css"), "utf8");
    const tokens = [...css.matchAll(/--radius(-[\w-]+)?\s*:\s*([^;]+);/g)];
    expect(tokens.length).toBeGreaterThan(0);
    for (const [, , value] of tokens) {
      expect(value.trim()).toBe("0");
    }
  });
});
