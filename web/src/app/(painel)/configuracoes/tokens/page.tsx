"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { PanelState } from "@/components/shell/page-header";
import { Spinner } from "@/components/ui";

/** Endereço antigo de "Tokens de API": a tela agora se chama Conexões. */
export default function TokensRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/configuracoes/conexoes/");
  }, [router]);
  return (
    <PanelState>
      <Spinner size="sm" decorative />
      <span role="status">Redirecionando para Conexões…</span>
    </PanelState>
  );
}
