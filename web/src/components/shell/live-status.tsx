"use client";

import { useSyncExternalStore } from "react";
import { cn } from "@/lib/cn";
import { getLiveSocket, useLive, type LiveStatus as Status } from "@/lib/ws";

const labels: Record<Status, { text: string; dot: string; fg: string }> = {
  open: { text: "Ao vivo", dot: "bg-success", fg: "text-success" },
  connecting: { text: "Conectando", dot: "bg-warning motion-safe:animate-pulse", fg: "text-warning" },
  closed: { text: "Desconectado", dot: "bg-danger", fg: "text-danger" },
};

const subscribe = (cb: () => void) => getLiveSocket().onStatus(cb);
const getStatus = () => getLiveSocket().status;
const getServerStatus = (): Status => "closed";

/** Indicador do WebSocket. Mantém uma inscrição em "system" para a conexão ficar aberta no painel. */
export function LiveStatus({ className }: { className?: string }) {
  useLive("system");
  const status = useSyncExternalStore(subscribe, getStatus, getServerStatus);
  const l = labels[status];
  return (
    <span
      role="status"
      data-status={status}
      className={cn(
        "inline-flex h-7 items-center gap-2 border border-line bg-bg px-2 font-mono text-[11px] uppercase tracking-[0.08em]",
        l.fg,
        className,
      )}
    >
      <span aria-hidden="true" className={cn("size-1.5 shrink-0", l.dot)} />
      <span className="sr-only">Conexão em tempo real: </span>
      <span>{l.text}</span>
    </span>
  );
}
