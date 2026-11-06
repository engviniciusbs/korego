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

/**
 * Buffer de linhas de log. `ts` é um texto de 19 dígitos: comparado como texto.
 * Junção sem duplicar: assinar o tópico, guardar o que chegar, buscar o tail
 * pela REST, setTail e depois append com o que foi guardado.
 */
export class LogBuffer {
  lines: LogLine[] = [];
  private lastTs = "";
  private readonly max: number;

  constructor(max = 5000) {
    this.max = max;
  }

  setTail(lines: LogLine[]): void {
    this.lines = [];
    this.lastTs = "";
    this.append(lines);
  }

  append(lines: LogLine[]): void {
    for (const l of lines) {
      if (l.ts <= this.lastTs) continue;
      this.lines.push(l);
      this.lastTs = l.ts;
    }
    if (this.lines.length > this.max) this.lines = this.lines.slice(this.lines.length - this.max);
  }

  filter(query: string): LogLine[] {
    const q = query.toLowerCase();
    if (!q) return this.lines;
    return this.lines.filter((l) => l.text.toLowerCase().includes(q));
  }
}
