export interface LogLine {
  ts: string;
  stream: string;
  text: string;
}

const ANSI_RE = /\u001b\[[0-9;?]*[ -/]*[@-~]/g;

/** Remove sequências ANSI (cores, cursor) e mantém o texto. */
export function stripAnsi(text: string): string {
  return text.replace(ANSI_RE, "");
}

/** Linha no buffer: `seq` cresce a cada linha aceita e serve de chave estável. */
export interface BufferedLine extends LogLine {
  seq: number;
}

/** Conjunto de linhas indexado por `ts`; igualdade por ts, stream e texto. */
class LineSet {
  private byTs = new Map<string, LogLine[]>();

  constructor(lines: Iterable<LogLine> = []) {
    for (const l of lines) this.add(l);
  }

  add(l: LogLine): void {
    const same = this.byTs.get(l.ts);
    if (same) same.push(l);
    else this.byTs.set(l.ts, [l]);
  }

  has(l: LogLine): boolean {
    return this.byTs.get(l.ts)?.some((x) => x.stream === l.stream && x.text === l.text) ?? false;
  }
}

/**
 * Buffer de linhas de log. O Docker marca stdout e stderr em goroutines
 * separadas, então `ts` não é monotônico: nada é descartado por ts.
 *
 * Junção sem duplicar: assinar o tópico, guardar o que chegar, buscar o tail
 * pela REST e chamar setTail(tail, guardadas). As linhas do tail ficam como
 * referência da junção: o que chegar depois e for igual a uma delas (o
 * primeiro stream do servidor reenvia as últimas linhas) é descartado. Fora
 * isso, as linhas ao vivo entram como vieram; o servidor não repete linhas.
 */
export class LogBuffer {
  lines: BufferedLine[] = [];
  private seq = 0;
  private joined = new LineSet();
  private readonly max: number;

  constructor(max = 5000) {
    this.max = max;
  }

  /** Troca o conteúdo pelo tail e acrescenta as pendentes que não estão nele. */
  setTail(tail: LogLine[], pending: LogLine[] = []): void {
    this.lines = [];
    this.joined = new LineSet(tail);
    this.push(tail);
    this.append(pending);
  }

  /**
   * Reconexão: acrescenta as linhas do tail novo que ainda não estão no
   * buffer (o buraco da queda) e depois as pendentes que não estão no tail.
   */
  mergeTail(tail: LogLine[], pending: LogLine[] = []): void {
    const have = new LineSet(this.lines);
    this.joined = new LineSet(tail);
    this.push(tail.filter((l) => !have.has(l)));
    this.append(pending);
  }

  append(lines: LogLine[]): void {
    this.push(lines.filter((l) => !this.joined.has(l)));
  }

  private push(lines: LogLine[]): void {
    for (const l of lines) this.lines.push({ ts: l.ts, stream: l.stream, text: l.text, seq: ++this.seq });
    if (this.lines.length > this.max) this.lines = this.lines.slice(this.lines.length - this.max);
  }

  filter(query: string): BufferedLine[] {
    const q = query.toLowerCase();
    if (!q) return this.lines;
    return this.lines.filter((l) => l.text.toLowerCase().includes(q));
  }
}
