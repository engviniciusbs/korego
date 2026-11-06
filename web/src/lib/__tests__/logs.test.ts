import { describe, expect, it } from "vitest";
import { LogBuffer, stripAnsi, type LogLine } from "../logs";

const L = (ts: string, text = ts): LogLine => ({ ts, stream: "stdout", text });
const ts = (n: number) => String(n).padStart(19, "0");

describe("LogBuffer", () => {
  const texts = (b: LogBuffer) => b.lines.map((l) => l.text);

  it("append mantém linhas com ts fora de ordem (stdout e stderr intercalados)", () => {
    const b = new LogBuffer();
    b.append([L(ts(5), "a"), { ts: ts(3), stream: "stderr", text: "b" }, L(ts(6), "c")]);
    b.append([{ ts: ts(4), stream: "stderr", text: "d" }, L(ts(4), "e"), L(ts(7), "f")]);
    expect(texts(b)).toEqual(["a", "b", "c", "d", "e", "f"]);
  });

  it("setTail descarta das pendentes só o que já está no tail", () => {
    const b = new LogBuffer();
    b.setTail([L(ts(1)), L(ts(2)), L(ts(3), "x")], [L(ts(2)), L(ts(3), "x"), L(ts(3), "y"), L(ts(1), "z"), L(ts(4))]);
    expect(texts(b)).toEqual([ts(1), ts(2), "x", "y", "z", ts(4)]);
  });

  it("linhas ao vivo repetidas do tail são descartadas, as novas não", () => {
    const b = new LogBuffer();
    b.setTail([L(ts(1)), L(ts(2))], []);
    // O primeiro stream do servidor reenvia as últimas linhas depois da junção.
    b.append([L(ts(1)), L(ts(2)), L(ts(2), "outra"), L(ts(1), "atrasada")]);
    expect(texts(b)).toEqual([ts(1), ts(2), "outra", "atrasada"]);
  });

  it("mergeTail acrescenta só o que faltava e depois as pendentes novas", () => {
    const b = new LogBuffer();
    b.setTail([L(ts(1)), L(ts(2))], []);
    b.append([L(ts(3))]);
    // Reconexão: o tail novo cobre o buraco (4, 5); pendentes repetem 5 e trazem 6.
    b.mergeTail([L(ts(2)), L(ts(3)), L(ts(4)), L(ts(5))], [L(ts(5)), L(ts(6))]);
    expect(texts(b)).toEqual([ts(1), ts(2), ts(3), ts(4), ts(5), ts(6)]);
  });

  it("seq é único e crescente mesmo com ts igual", () => {
    const b = new LogBuffer();
    b.setTail([L(ts(1), "a"), L(ts(1), "b")], [L(ts(1), "c")]);
    b.append([L(ts(1), "d")]);
    const seqs = b.lines.map((l) => l.seq);
    expect(new Set(seqs).size).toBe(4);
    expect([...seqs].sort((x, y) => x - y)).toEqual(seqs);
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
