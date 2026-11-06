export interface ChartPoint {
  t: number;
  v: number;
}

/** Corta a série onde a distância entre pontos passa de 2 × stepSeconds. */
export function buildSegments(points: ChartPoint[], stepSeconds: number): ChartPoint[][] {
  const segs: ChartPoint[][] = [];
  let cur: ChartPoint[] = [];
  for (const p of points) {
    const prev = cur[cur.length - 1];
    if (prev && p.t - prev.t > 2 * stepSeconds) {
      segs.push(cur);
      cur = [];
    }
    cur.push(p);
  }
  if (cur.length > 0) segs.push(cur);
  return segs;
}

/** Arredonda o teto do eixo para 1, 2 ou 5 × 10^n (1 para v <= 0). */
export function niceMax(v: number): number {
  if (!Number.isFinite(v) || v <= 0) return 1;
  const exp = Math.floor(Math.log10(v));
  const base = 10 ** exp;
  const f = v / base;
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
  return Number((nice * base).toPrecision(12));
}
