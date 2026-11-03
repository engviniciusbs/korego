// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { compile } from "@tailwindcss/node";
import { describe, expect, it } from "vitest";
import { focusRing } from "@/components/ui/focus";

const APP = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "app");

/** Gera o CSS real do Tailwind (com o tema de globals.css) só para as classes dadas. */
async function buildCss(classes: string[]): Promise<string> {
  const css = readFileSync(join(APP, "globals.css"), "utf8");
  const compiler = await compile(css, { base: APP, onDependency: () => {} });
  return compiler.build(classes);
}

/**
 * Resolve o outline-style efetivo de um elemento com foco visível e todas as classes.
 * Regras com :focus-visible vêm depois das regras base na camada utilities,
 * então sobrescrevem; `var(--tw-outline-style)` cai no valor da variável.
 */
function focusOutlineStyle(css: string): string | undefined {
  // Só o bloco `@layer utilities { ... }` (o CSS seguinte tem fallbacks globais de variáveis).
  const start = css.indexOf("{", css.indexOf("@layer utilities"));
  let depth = 0;
  let end = start;
  for (; end < css.length; end++) {
    if (css[end] === "{") depth++;
    if (css[end] === "}" && --depth === 0) break;
  }
  const utilities = css
    .slice(start + 1, end)
    // Blocos aninhados (ex.: @media forced-colors do outline-hidden) não valem fora desse modo.
    .replace(/@media[^{]*\{[^{}]*\}/g, "");
  const decls = { base: new Map<string, string>(), focus: new Map<string, string>() };
  for (const [, selector, body] of utilities.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const target = selector.includes(":focus-visible") ? decls.focus : decls.base;
    for (const [, prop, value] of body.matchAll(/([\w-]+)\s*:\s*([^;]+);/g)) {
      target.set(prop, value.trim());
    }
  }
  const pick = (prop: string) => decls.focus.get(prop) ?? decls.base.get(prop);
  const style = pick("outline-style");
  if (!style) return undefined;
  if (style.startsWith("var(--tw-outline-style")) return pick("--tw-outline-style") ?? "solid";
  return style;
}

describe("anel de foco", () => {
  it("focusRing não zera o outline-style que o focus-visible usa", async () => {
    expect(focusRing).not.toMatch(/\boutline-none\b/);
    const css = await buildCss(focusRing.split(/\s+/));
    expect(focusOutlineStyle(css)).toBe("solid");
  });

  it("detecta a regressão: outline-none + outline-2 resolve para none", async () => {
    const css = await buildCss(["outline-none", "focus-visible:outline-2"]);
    expect(focusOutlineStyle(css)).toBe("none");
  });

  it("nenhum componente usa outline-none (some no modo de alto contraste)", () => {
    const files = ["button.tsx", "input.tsx", "focus.ts"].map((f) =>
      readFileSync(join(APP, "..", "components", "ui", f), "utf8"),
    );
    for (const src of files) expect(src).not.toMatch(/\boutline-none\b/);
  });
});
