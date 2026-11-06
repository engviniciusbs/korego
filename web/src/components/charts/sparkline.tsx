import { cn } from "@/lib/cn";

export const SPARK_SLOTS = 60;

export interface SparklineProps {
  /** Valores mais recentes (até 60, um a cada 2 s); o último fica na borda direita. */
  values: number[];
  /** Teto da escala; sem ele usa o maior valor da janela. */
  max?: number;
  color?: string;
  className?: string;
}

const W = SPARK_SLOTS - 1;
const H = 24;

/** Linha mínima dos últimos 2 minutos. Decorativa: o número ao lado carrega o valor. */
export function Sparkline({ values, max, color = "var(--color-series-1)", className }: SparklineProps) {
  const vals = values.slice(-SPARK_SLOTS);
  const top = max && max > 0 ? max : Math.max(...vals, 0) || 1;
  const offset = SPARK_SLOTS - vals.length;
  const d = vals
    .map((v, i) => {
      const y = H - (Math.min(Math.max(v, 0), top) / top) * (H - 2) - 1;
      return `${i ? "L" : "M"}${offset + i} ${y.toFixed(2)}`;
    })
    .join("");
  return (
    <svg
      aria-hidden="true"
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      className={cn("block h-6 w-full", className)}
    >
      <line x1={0} x2={W} y1={H - 0.5} y2={H - 0.5} stroke="var(--color-line)" vectorEffect="non-scaling-stroke" />
      {vals.length > 1 && (
        <path
          d={d}
          fill="none"
          stroke={color}
          strokeWidth={1.5}
          strokeLinejoin="miter"
          strokeLinecap="butt"
          vectorEffect="non-scaling-stroke"
        />
      )}
    </svg>
  );
}
