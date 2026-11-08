import { describe, expect, it } from "vitest";
import { DeployLogBuffer, isActive, statusLabel } from "../deploys";

const e = (n: number) => ({ n, text: `linha ${n}` });

describe("DeployLogBuffer", () => {
  it("junta por n sem duplicar", () => {
    const b = new DeployLogBuffer();
    b.setInitial([e(1), e(2), e(3)]);
    b.append([e(3), e(4), e(2)]);
    expect(b.lines.map((l) => l.n)).toEqual([1, 2, 3, 4]);
    expect(b.needsRefetch).toBe(false);
  });

  it("detecta lacuna e pede refetch a partir do último n contíguo", () => {
    const b = new DeployLogBuffer();
    b.setInitial([e(1), e(2)]);
    b.append([e(5)]);
    expect(b.needsRefetch).toBe(true);
    expect(b.refetchFrom).toBe(2);
    expect(b.lines.map((l) => l.n)).toEqual([1, 2]);
    b.setInitial([e(3), e(4), e(5)]);
    expect(b.needsRefetch).toBe(false);
    expect(b.lines.map((l) => l.n)).toEqual([1, 2, 3, 4, 5]);
  });

  it("ordena por n e aceita lote fora de ordem", () => {
    const b = new DeployLogBuffer();
    b.append([e(2), e(1)]);
    expect(b.lines.map((l) => l.n)).toEqual([1, 2]);
  });

  it("limita a 10000 descartando as mais antigas", () => {
    const b = new DeployLogBuffer();
    const all = Array.from({ length: 10005 }, (_, i) => e(i + 1));
    b.setInitial(all);
    expect(b.lines).toHaveLength(10000);
    expect(b.lines[0].n).toBe(6);
    b.append([e(3)]); // já descartada: não volta
    expect(b.lines[0].n).toBe(6);
    expect(b.needsRefetch).toBe(false);
  });

  it("markDone", () => {
    const b = new DeployLogBuffer();
    expect(b.done).toBe(false);
    b.markDone();
    expect(b.done).toBe(true);
  });
});

describe("statusLabel e isActive", () => {
  it("rótulos em pt-BR", () => {
    expect(statusLabel("queued")).toBe("Na fila");
    expect(statusLabel("building")).toBe("Construindo");
    expect(statusLabel("starting")).toBe("Subindo");
    expect(statusLabel("running")).toBe("No ar");
    expect(statusLabel("failed")).toBe("Falhou");
    expect(statusLabel("superseded")).toBe("Substituído");
    expect(statusLabel("cancelled")).toBe("Cancelado");
    expect(statusLabel("xyz")).toBe("xyz");
  });
  it("isActive", () => {
    for (const s of ["queued", "building", "starting"]) expect(isActive(s)).toBe(true);
    for (const s of ["running", "failed", "superseded", "cancelled"]) expect(isActive(s)).toBe(false);
  });
});
