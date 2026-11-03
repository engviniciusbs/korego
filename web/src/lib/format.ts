/** Uptime em segundos → "3d 4h 12m" (omite unidades maiores zeradas). */
export function formatUptime(seconds: number): string {
  const s = Number.isFinite(seconds) && seconds > 0 ? Math.floor(seconds) : 0;
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

const gibFormat = new Intl.NumberFormat("pt-BR", {
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
});

/** Bytes → "7,8 GiB". */
export function formatGiB(bytes: number): string {
  return `${gibFormat.format(bytes / 1024 ** 3)} GiB`;
}

/** Data RFC3339 → "03/11/2026, 14:05" (pt-BR). Texto inválido volta como veio. */
export function formatDateTime(iso: string, timeZone?: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return new Intl.DateTimeFormat("pt-BR", {
    dateStyle: "short",
    timeStyle: "short",
    timeZone,
  }).format(d);
}

/** User-Agent → "Chrome · Windows". Sem reconhecimento, devolve o texto bruto. */
export function describeUserAgent(ua: string): string {
  if (!ua.trim()) return "Desconhecido";
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /OPR\//.test(ua)
      ? "Opera"
      : /Firefox\//.test(ua)
        ? "Firefox"
        : /Chrome\//.test(ua)
          ? "Chrome"
          : /Version\/.*Safari\//.test(ua)
            ? "Safari"
            : "";
  const os = /Windows/.test(ua)
    ? "Windows"
    : /Android/.test(ua)
      ? "Android"
      : /iPhone|iPad/.test(ua)
        ? "iOS"
        : /Mac OS X|Macintosh/.test(ua)
          ? "macOS"
          : /Linux/.test(ua)
            ? "Linux"
            : "";
  if (!browser) return ua;
  return os ? `${browser} · ${os}` : browser;
}
