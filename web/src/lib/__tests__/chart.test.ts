import { describe, expect, it } from "vitest";
import { buildSegments, niceMax } from "../chart";

describe("buildSegments", () => {
  it("corta onde falta mais de 2 passos", () => {
    const p = [0, 60, 120, 300, 360].map((t) => ({ t, v: 1 }));
    const segs = buildSegments(p, 60);
    expect(segs.map((s) => s.map((x) => x.t))).toEqual([[0, 60, 120], [300, 360]]);
  });

  it("exatamente 2 passos não corta; vazio devolve []", () => {
    expect(buildSegments([{ t: 0, v: 1 }, { t: 120, v: 1 }], 60)).toHaveLength(1);
    expect(buildSegments([], 60)).toEqual([]);
  });
});

describe("niceMax", () => {
  it("73 → 100, 0.4 → 0.5, 0 → 1", () => {
    expect(niceMax(73)).toBe(100);
    expect(niceMax(0.4)).toBe(0.5);
    expect(niceMax(0)).toBe(1);
  });

  it("valores exatos e intermediários", () => {
    expect(niceMax(100)).toBe(100);
    expect(niceMax(1.5)).toBe(2);
    expect(niceMax(3)).toBe(5);
    expect(niceMax(-4)).toBe(1);
  });
});
