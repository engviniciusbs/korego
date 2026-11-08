"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import type { CertInfo } from "./domains";

/** GET /api/settings/panel. */
export interface PanelSettings {
  domain: string;
  domainId: number;
  cert: CertInfo;
  publicPort: boolean;
  listenAddr: string;
  requestHTTPS: boolean;
  requestHost: string;
}

/** PUT /settings/panel/domain: 200 com o painel, ou `saved` com falha do proxy. */
export type SetPanelDomainResult = PanelSettings | { saved: true; proxyError: string };

export function usePanelSettings() {
  return useQuery({
    queryKey: ["panel"],
    queryFn: () => api<PanelSettings>("/api/settings/panel"),
  });
}

function invalidatePanel(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: ["panel"] });
  void qc.invalidateQueries({ queryKey: ["domains"] });
  void qc.invalidateQueries({ queryKey: ["proxy"] });
}

export function useSetPanelDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (hostname: string) =>
      api<SetPanelDomainResult>("/api/settings/panel/domain", { method: "PUT", body: { hostname } }),
    onSettled: () => {
      invalidatePanel(qc);
      // O GitHub App depende do domínio do painel (panelReady).
      void qc.invalidateQueries({ queryKey: ["github"] });
    },
  });
}

/** Erros PANEL_NOT_HTTPS e PANEL_PORT_OFF vêm do servidor. */
export function useSetPublicPort() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) =>
      api<PanelSettings>("/api/settings/panel/public-port", { method: "PUT", body: { enabled } }),
    onSettled: () => invalidatePanel(qc),
  });
}
