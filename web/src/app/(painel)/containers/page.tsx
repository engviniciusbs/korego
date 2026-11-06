"use client";

import { useId, useMemo, useState } from "react";
import { ContainerTable } from "@/components/containers/container-table";
import { PageHeader, PanelState } from "@/components/shell/page-header";
import { Alert, Button, Input, Label, Panel, Spinner } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { groupContainers, useContainers, useContainersLive } from "@/lib/containers";

export default function ContainersPage() {
  const { data, isPending, isError, error, refetch, isFetching } = useContainers();
  useContainersLive();
  const [filter, setFilter] = useState("");
  const query = filter.trim().toLocaleLowerCase("pt-BR");
  const filterId = useId();

  const groups = useMemo(() => {
    const list = data ?? [];
    const visible = query ? list.filter((c) => c.name.toLocaleLowerCase("pt-BR").includes(query)) : list;
    return groupContainers(visible);
  }, [data, query]);

  const total = data?.length ?? 0;
  const running = data?.filter((c) => c.state === "running").length ?? 0;
  const unavailable =
    isError && error instanceof ApiError && (error.code === "DOCKER_UNAVAILABLE" || error.status === 503);

  return (
    <>
      <PageHeader
        eyebrow="Docker"
        title="Containers"
        description={
          data
            ? `${total} ${total === 1 ? "container" : "containers"}, ${running} rodando. A lista se atualiza sozinha.`
            : "Todos os containers do servidor, agrupados por projeto compose."
        }
        actions={
          data && total > 0 ? (
            <div className="w-64 max-w-full space-y-1">
              <Label htmlFor={filterId}>Filtrar por nome</Label>
              <Input
                id={filterId}
                type="search"
                name="filtro"
                size="sm"
                mono
                autoComplete="off"
                spellCheck={false}
                placeholder="ex.: web…"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
              />
            </div>
          ) : undefined
        }
      />

      {isPending ? (
        <Panel>
          <PanelState>
            <Spinner size="sm" decorative />
            <span role="status">Consultando o Docker…</span>
          </PanelState>
        </Panel>
      ) : isError ? (
        <Alert
          variant={unavailable ? "warning" : "danger"}
          title={unavailable ? "Docker indisponível" : "Não foi possível listar os containers."}
        >
          <p>
            {unavailable
              ? "O daemon do Docker não respondeu. Verifique se ele está em execução no servidor."
              : error.message}
          </p>
          <Button size="sm" className="mt-2" loading={isFetching} onClick={() => void refetch()}>
            Tentar novamente
          </Button>
        </Alert>
      ) : total === 0 ? (
        <Panel>
          <PanelState>Nenhum container neste servidor.</PanelState>
        </Panel>
      ) : groups.length === 0 ? (
        <Panel>
          <PanelState>
            <span role="status">Nenhum container com “{filter.trim()}” no nome.</span>
          </PanelState>
        </Panel>
      ) : (
        <div className="space-y-6">
          {groups.map((g) => (
            <ContainerTable key={g.project ?? "\u0000avulsos"} group={g} />
          ))}
        </div>
      )}
    </>
  );
}
