import { describe, expect, it } from "vitest";
import { LogBuffer, stripAnsi, type LogLine } from "../logs";

const L = (ts: string, text = ts): LogLine => ({ ts, stream: "stdout", text });
const ts = (n: number) => String(n).padStart(19, "0");

describe("LogBuffer", () => {
  it("append descarta ts repetido ou antigo", () => {
    const b = new LogBuffer();
    b.append([L(ts(5)), L(ts(6))]);
    b.append([L(ts(6)), L(ts(4)), L(ts(7))]);
    expect(b.lines.map((l) => l.ts)).toEqual([ts(5), ts(6), ts(7)]);
  });

  it("setTail + linhas guardadas do WS sem duplicar a junção", () => {
    const b = new LogBuffer();
    b.setTail([L(ts(1)), L(ts(2)), L(ts(3))]);
    b.append([L(ts(2)), L(ts(3)), L(ts(4))]);
    expect(b.lines.map((l) => l.ts)).toEqual([ts(1), ts(2), ts(3), ts(4)]);
  });

  it("mantém no máximo 5000 linhas, descartando as mais antigas", () => {
    const b = new LogBuffer();
    b.append(Array.from({ length: 5100 }, (_, i) => L(ts(i + 1))));
    expect(b.lines).toHaveLength(5000);
    expect(b.lines[0].ts).toBe(ts(101));
    expect(b.lines[4999].ts).toBe(ts(5100));
  });

  it("filter ignora maiúsculas", () => {
    const b = new LogBuffer();
    b.append([L(ts(1), "Erro grave"), L(ts(2), "ok")]);
    expect(b.filter("ERRO")).toHaveLength(1);
    expect(b.filter("")).toHaveLength(2);
  });
});

describe("stripAnsi", () => {
  it("remove códigos de cor e mantém o texto", () => {
    expect(stripAnsi("\u001b[31mfalha\u001b[0m ok \u001b[1;32mverde\u001b[0m")).toBe("falha ok verde");
  });
});
