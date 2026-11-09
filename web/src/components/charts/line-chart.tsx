"use client";

import { useMemo, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent } from "react";
import { buildSegments, niceMax, type ChartPoint } from "@/lib/chart";
import { cn } from "@/lib/cn";
import { focusRing } from "@/components/ui/focus";

export interface LineSeries {
  label: string;
  points: ChartPoint[];
  /** Cor da série; por padrão segue a ordem fixa `--color-series-N`. */
  color?: string;
}

export interface LineChartProps {
  series: LineSeries[];
  stepSeconds: number;
  format: (v: number) => string;
  /** Teto do eixo Y; sem ele o teto é `niceMax` do maior valor. */
  yMax?: number;
  /**
   * Janela do eixo X em segundos unix ([de, até]). Com ela, períodos sem dados
   * no começo ou no fim aparecem como vazio, e pontos fora da janela são cortados.
   * Sem ela, o eixo vai do primeiro ao último ponto.
   */
  domain?: [number, number];
  /** Altura da área do gráfico em px. */
  height?: number;
  className?: string;
  /**
   * Clique (ou Enter com o foco no gráfico) num ponto: recebe o `t` do ponto.
   * Com ela o gráfico ganha cursor de clique e a dica da ação.
   */
  onPointClick?: (t: number) => void;
  /** O que o clique faz, para a dica e o rótulo acessível. Ex.: "ver quem estava usando". */
  pointActionLabel?: string;
}

const SERIES_COLORS = ["var(--color-series-1)", "var(--color-series-2)"];

// Sistema de coordenadas do SVG: esticado na largura (preserveAspectRatio="none");
// os traços usam vector-effect="non-scaling-stroke" para não deformar.
const W = 1000;
const H = 100;

const timeShort = new Intl.DateTimeFormat("pt-BR", { hour: "2-digit", minute: "2-digit" });
const timeLong = new Intl.DateTimeFormat("pt-BR", {
  day: "2-digit",
  month: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
});

/** Hora de um ponto no formato do tooltip do gráfico ("06/11, 12:00"). */
export const formatChartTime = (t: number) => timeLong.format(new Date(t * 1000));

const colorOf = (s: LineSeries, i: number) => s.color ?? SERIES_COLORS[i % SERIES_COLORS.length];

/** Gráfico de linha em SVG próprio: buracos viram cortes, eixo Y com teto "redondo". */
export function LineChart({
  series: rawSeries,
  stepSeconds,
  format,
  yMax,
  domain,
  height = 160,
  className,
  onPointClick,
  pointActionLabel = "ver os detalhes deste momento",
}: LineChartProps) {
  const [hover, setHover] = useState<number | null>(null);
  // Ponto acionado por último: quando o foco volta ao gráfico (ex.: ao fechar
  // o diálogo aberto pelo clique), a leitura recomeça nele e não no fim.
  const picked = useRef<number | null>(null);

  const d0 = domain?.[0];
  const d1 = domain?.[1];
  const series = useMemo(
    () =>
      d0 === undefined || d1 === undefined
        ? rawSeries
        : rawSeries.map((s) => ({ ...s, points: s.points.filter((p) => p.t >= d0 && p.t <= d1) })),
    [rawSeries, d0, d1],
  );

  const model = useMemo(() => {
    const times = [...new Set(series.flatMap((s) => s.points.map((p) => p.t)))].sort((a, b) => a - b);
    if (times.length === 0) return null;
    let t0 = d0 !== undefined && d1 !== undefined && d1 > d0 ? d0 : times[0];
    let t1 = d0 !== undefined && d1 !== undefined && d1 > d0 ? d1 : times[times.length - 1];
    if (t0 === t1) {
      t0 -= stepSeconds;
      t1 += stepSeconds;
    }
    const maxV = Math.max(0, ...series.flatMap((s) => s.points.map((p) => p.v)));
    const top = yMax && yMax > 0 ? Math.max(yMax, maxV) : niceMax(maxV);
    const x = (t: number) => ((t - t0) / (t1 - t0)) * W;
    const y = (v: number) => H - (Math.min(Math.max(v, 0), top) / top) * H;
    // Meio passo de largura (mínimo visível) para o segmento de um ponto só.
    const tick = Math.max(((stepSeconds / (t1 - t0)) * W) / 2, 4);
    const byT = series.map((s) => new Map(s.points.map((p) => [p.t, p.v])));
    const paths = series.map((s) =>
      buildSegments(s.points, stepSeconds).map((seg) =>
        seg.length === 1
          ? { single: true, d: `M${(x(seg[0].t) - tick).toFixed(2)} ${y(seg[0].v).toFixed(2)}H${(x(seg[0].t) + tick).toFixed(2)}` }
          : { single: false, d: seg.map((p, k) => `${k ? "L" : "M"}${x(p.t).toFixed(2)} ${y(p.v).toFixed(2)}`).join("") },
      ),
    );
    // Data no eixo quando a janela cobre um dia inteiro ou cruza a meia-noite.
    const long =
      t1 - t0 >= 86400 || new Date(t0 * 1000).toDateString() !== new Date(t1 * 1000).toDateString();
    return { times, t0, t1, top, x, y, byT, paths, long };
  }, [series, stepSeconds, yMax, d0, d1]);

  const summary = series
    .map((s) => {
      const last = s.points[s.points.length - 1];
      return `${s.label}: ${last ? format(last.v) : "sem dados"}`;
    })
    .join(", ");

  const legend = series.length > 1 && (
    <ul className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-fg-muted">
      {series.map((s, i) => (
        <li key={s.label} className="flex items-center gap-1.5">
          <span aria-hidden="true" className="h-0.5 w-3" style={{ background: colorOf(s, i) }} />
          {s.label}
        </li>
      ))}
    </ul>
  );

  if (!model) {
    return (
      <div className={className}>
        {legend}
        <div
          className="flex items-center justify-center border border-dashed border-line text-[13px] text-fg-subtle"
          style={{ height }}
        >
          Sem dados neste período.
        </div>
      </div>
    );
  }

  const { times, t0, t1, top, x, y, byT, paths, long } = model;
  const fmtAxis = (t: number) => (long ? timeLong : timeShort).format(new Date(t * 1000));

  const nearest = (t: number) => {
    let lo = 0;
    let hi = times.length - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (times[mid] < t) lo = mid + 1;
      else hi = mid;
    }
    return lo > 0 && t - times[lo - 1] < times[lo] - t ? lo - 1 : lo;
  };

  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    if (r.width <= 0) return;
    const frac = Math.min(Math.max((e.clientX - r.left) / r.width, 0), 1);
    setHover(nearest(t0 + frac * (t1 - t0)));
  };

  const pick = (i: number | null) => {
    if (!onPointClick || i === null || i >= times.length) return;
    picked.current = times[i];
    onPointClick(times[i]);
  };

  const onClick = (e: MouseEvent<HTMLDivElement>) => {
    if (!onPointClick) return;
    const r = e.currentTarget.getBoundingClientRect();
    if (r.width > 0) {
      const frac = Math.min(Math.max((e.clientX - r.left) / r.width, 0), 1);
      const i = nearest(t0 + frac * (t1 - t0));
      setHover(i);
      pick(i);
    } else {
      pick(hover);
    }
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Enter" && onPointClick) {
      e.preventDefault();
      pick(hover ?? times.length - 1);
      return;
    }
    const cur = hover ?? times.length - 1;
    const next =
      e.key === "ArrowLeft"
        ? Math.max(cur - 1, 0)
        : e.key === "ArrowRight"
          ? Math.min(cur + 1, times.length - 1)
          : e.key === "Home"
            ? 0
            : e.key === "End"
              ? times.length - 1
              : null;
    if (next === null) return;
    e.preventDefault();
    setHover(next);
  };

  // O índice pode sobrar de outro período (menos pontos): ignora.
  const ht = hover === null || hover >= times.length ? null : times[hover];
  const hx = ht === null ? 0 : (x(ht) / W) * 100;
  const ticks = [top, top / 2, 0];

  return (
    <div className={cn("text-fg", className)}>
      {legend}
      <div className="flex" style={{ height }}>
        <div aria-hidden="true" className="relative w-[4.5rem] shrink-0">
          {ticks.map((v) => (
            <span
              key={v}
              className="absolute right-2 -translate-y-1/2 font-mono text-[10px] whitespace-nowrap tabular-nums text-fg-subtle"
              style={{ top: `${(y(v) / H) * 100}%` }}
            >
              {format(v)}
            </span>
          ))}
        </div>
        <div
          role="group"
          tabIndex={0}
          aria-label={
            onPointClick
              ? `${summary}. Use as setas para percorrer os valores e Enter para ${pointActionLabel}.`
              : `${summary}. Use as setas para percorrer os valores.`
          }
          className={cn("relative min-w-0 flex-1 touch-pan-y", onPointClick && "cursor-pointer", focusRing)}
          onPointerMove={onPointerMove}
          onPointerLeave={() => setHover(null)}
          onClick={onPointClick ? onClick : undefined}
          onFocus={() =>
            setHover((h) => {
              if (h !== null) return h;
              const back = picked.current;
              picked.current = null;
              return back === null ? times.length - 1 : nearest(back);
            })
          }
          onBlur={() => setHover(null)}
          onKeyDown={onKeyDown}
        >
          <svg
            role="img"
            aria-label={`Último valor: ${summary}`}
            viewBox={`0 0 ${W} ${H}`}
            preserveAspectRatio="none"
            className="absolute inset-0 size-full overflow-visible"
          >
            {ticks.map((v) => (
              <line
                key={v}
                x1={0}
                x2={W}
                y1={y(v)}
                y2={y(v)}
                stroke="var(--color-line)"
                strokeWidth={1}
                vectorEffect="non-scaling-stroke"
              />
            ))}
            {paths.map((segs, i) =>
              segs.map((s, k) => (
                <path
                  key={`${i}-${k}`}
                  data-segment=""
                  data-single={s.single ? "" : undefined}
                  d={s.d}
                  fill="none"
                  stroke={colorOf(series[i], i)}
                  strokeWidth={2}
                  strokeLinejoin="miter"
                  strokeLinecap="butt"
                  vectorEffect="non-scaling-stroke"
                />
              )),
            )}
            {ht !== null && (
              <line
                x1={x(ht)}
                x2={x(ht)}
                y1={0}
                y2={H}
                stroke="var(--color-fg-subtle)"
                strokeWidth={1}
                vectorEffect="non-scaling-stroke"
              />
            )}
          </svg>

          {ht !== null &&
            series.map((s, i) => {
              const v = byT[i].get(ht);
              if (v === undefined) return null;
              return (
                <span
                  key={s.label}
                  aria-hidden="true"
                  className="pointer-events-none absolute size-2 -translate-1/2 border-2 border-surface"
                  style={{ left: `${hx}%`, top: `${(y(v) / H) * 100}%`, background: colorOf(s, i) }}
                />
              );
            })}

          <p className="sr-only" aria-live="polite">
            {ht === null
              ? ""
              : `${timeLong.format(new Date(ht * 1000))}: ${series
                  .map((s, i) => {
                    const v = byT[i].get(ht);
                    return `${s.label} ${v === undefined ? "sem dados" : format(v)}`;
                  })
                  .join(", ")}`}
          </p>

          {ht !== null && (
            <div
              aria-hidden="true"
              className={cn(
                "pointer-events-none absolute top-1 z-10 min-w-36 border border-line-strong bg-surface-2 px-2.5 py-2 shadow-[0_8px_24px_-8px_rgba(0,0,0,0.8)]",
                hx > 55 ? "-translate-x-[calc(100%+10px)]" : "translate-x-[10px]",
              )}
              style={{ left: `${hx}%` }}
            >
              <p className="font-mono text-[10px] tabular-nums text-fg-subtle">
                {timeLong.format(new Date(ht * 1000))}
              </p>
              <ul className="mt-1 space-y-0.5">
                {series.map((s, i) => {
                  const v = byT[i].get(ht);
                  return (
                    <li key={s.label} className="flex items-center gap-2 text-[11px]">
                      <span aria-hidden="true" className="h-0.5 w-2.5 shrink-0" style={{ background: colorOf(s, i) }} />
                      <span className="font-mono font-semibold tabular-nums text-fg">
                        {v === undefined ? "—" : format(v)}
                      </span>
                      {series.length > 1 && <span className="text-fg-muted">{s.label}</span>}
                    </li>
                  );
                })}
              </ul>
              {onPointClick && (
                <p className="mt-1.5 border-t border-line pt-1.5 text-[10px] text-fg-subtle">
                  Clique para {pointActionLabel}
                </p>
              )}
            </div>
          )}
        </div>
      </div>
      <div
        aria-hidden="true"
        className="ml-[4.5rem] mt-1.5 flex justify-between font-mono text-[10px] tabular-nums text-fg-subtle"
      >
        <span>{fmtAxis(t0)}</span>
        <span>{fmtAxis(t1)}</span>
      </div>
    </div>
  );
}
