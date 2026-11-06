import { cn } from "@/lib/cn";

export type SpinnerSize = "sm" | "md" | "lg";

const sizes: Record<SpinnerSize, string> = {
  sm: "size-3 border-[1.5px]",
  md: "size-4 border-2",
  lg: "size-6 border-2",
};

export interface SpinnerProps {
  size?: SpinnerSize;
  /** Texto lido por leitores de tela. */
  label?: string;
  /** Quando true, o spinner é só visual (o contexto já anuncia o estado). */
  decorative?: boolean;
  className?: string;
}

export function Spinner({
  size = "md",
  label = "Carregando…",
  decorative = false,
  className,
}: SpinnerProps) {
  const ring = (
    <span
      aria-hidden="true"
      data-size={size}
      className={cn(
        "inline-block shrink-0 animate-spin border-current border-t-transparent",
        "motion-reduce:animate-[spin_1.6s_linear_infinite]",
        sizes[size],
        className,
      )}
    />
  );
  if (decorative) return ring;
  return (
    <span role="status" className="inline-flex items-center">
      {ring}
      <span className="sr-only">{label}</span>
    </span>
  );
}
