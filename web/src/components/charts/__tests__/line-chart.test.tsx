import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { LineChart } from "../line-chart";

const fmt = (v: number) => `${v} u`;

describe("LineChart", () => {
  it("LineChart desenha um path por segmento quando há buraco", () => {
    const points = [0, 60, 120, 600, 660].map((t, i) => ({ t, v: i + 1 }));
    const { container } = render(
      <LineChart series={[{ label: "CPU", points }]} stepSeconds={60} format={fmt} yMax={10} />,
    );
    expect(container.querySelectorAll("path[data-segment]")).toHaveLength(2);
    expect(screen.getByRole("img", { name: /CPU: 5 u/ })).toBeInTheDocument();
    // Eixo Y: teto informado.
    expect(screen.getByText("10 u")).toBeInTheDocument();
  });

  it("segmento de um ponto só continua visível", () => {
    const points = [0, 60, 600].map((t) => ({ t, v: 3 }));
    const { container } = render(
      <LineChart series={[{ label: "CPU", points }]} stepSeconds={60} format={fmt} />,
    );
    expect(container.querySelectorAll("path[data-segment]")).toHaveLength(2);
    expect(container.querySelectorAll("[data-single]")).toHaveLength(1);
  });

  it("teclado percorre os valores e mostra a leitura", () => {
    const points = [0, 60, 120].map((t, i) => ({ t, v: (i + 1) * 10 }));
    render(
      <LineChart
        series={[
          { label: "Entrada", points },
          { label: "Saída", points: points.map((p) => ({ ...p, v: p.v + 1 })) },
        ]}
        stepSeconds={60}
        format={fmt}
      />,
    );
    const area = screen.getByRole("group");
    fireEvent.focus(area);
    expect(screen.getByText("30 u")).toBeInTheDocument();
    expect(screen.getByText("31 u")).toBeInTheDocument();
    fireEvent.keyDown(area, { key: "ArrowLeft" });
    expect(screen.getByText("20 u")).toBeInTheDocument();
    expect(screen.getByText("21 u")).toBeInTheDocument();
  });

  it("com domain, pontos só nos últimos 10 minutos de 1h começam depois de 80% da largura", () => {
    // viewBox tem 1000 de largura; o primeiro x do path é a posição do primeiro ponto.
    const points = [3000, 3060, 3120, 3180].map((t) => ({ t, v: 1 }));
    const { container } = render(
      <LineChart
        series={[{ label: "CPU", points: [{ t: -600, v: 9 }, ...points] }]}
        stepSeconds={60}
        format={fmt}
        domain={[0, 3600]}
      />,
    );
    const paths = container.querySelectorAll("path[data-segment]");
    // O ponto fora da janela é cortado: sobra um segmento.
    expect(paths).toHaveLength(1);
    const firstX = Number(/^M([\d.]+)/.exec(paths[0].getAttribute("d") ?? "")?.[1]);
    expect(firstX).toBeGreaterThan(800);
    expect(firstX).toBeCloseTo((3000 / 3600) * 1000, 1);
  });

  it("janela de 24h mostra a data nos extremos do eixo", () => {
    const end = Date.UTC(2026, 10, 6, 15, 0) / 1000;
    const points = [end - 3600, end].map((t) => ({ t, v: 1 }));
    render(
      <LineChart
        series={[{ label: "CPU", points }]}
        stepSeconds={60}
        format={fmt}
        domain={[end - 86400, end]}
      />,
    );
    expect(screen.getAllByText(/\d{2}\/\d{2}, \d{2}:\d{2}/).length).toBeGreaterThanOrEqual(2);
  });

  it("LineChart chama onPointClick com o t do ponto (clique e Enter)", () => {
    const points = [0, 60, 120].map((t, i) => ({ t, v: (i + 1) * 10 }));
    const onPointClick = vi.fn();
    const { rerender } = render(
      <LineChart
        series={[{ label: "CPU", points }]}
        stepSeconds={60}
        format={fmt}
        onPointClick={onPointClick}
        pointActionLabel="ver quem estava usando"
      />,
    );
    const area = screen.getByRole("group");
    expect(area).toHaveClass("cursor-pointer");
    expect(area).toHaveAccessibleName(/Enter para ver quem estava usando/);

    // Clique: o ponto mais próximo da posição do ponteiro (aqui, o do meio).
    vi.spyOn(area, "getBoundingClientRect").mockReturnValue({
      left: 0,
      top: 0,
      width: 300,
      height: 100,
      right: 300,
      bottom: 100,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });
    fireEvent.click(area, { clientX: 140 });
    expect(onPointClick).toHaveBeenLastCalledWith(60);

    // Teclado: foco começa no último ponto; ← volta um; Enter dispara.
    fireEvent.focus(area);
    fireEvent.keyDown(area, { key: "End" });
    fireEvent.keyDown(area, { key: "Enter" });
    expect(onPointClick).toHaveBeenLastCalledWith(120);
    fireEvent.keyDown(area, { key: "Home" });
    fireEvent.keyDown(area, { key: "Enter" });
    expect(onPointClick).toHaveBeenLastCalledWith(0);
    expect(onPointClick).toHaveBeenCalledTimes(3);

    // Sem a prop: nada de cursor de clique e o Enter não faz nada.
    rerender(<LineChart series={[{ label: "CPU", points }]} stepSeconds={60} format={fmt} />);
    const plain = screen.getByRole("group");
    expect(plain).not.toHaveClass("cursor-pointer");
    expect(plain).not.toHaveAccessibleName(/Enter/);
    fireEvent.keyDown(plain, { key: "Enter" });
    fireEvent.click(plain);
    expect(onPointClick).toHaveBeenCalledTimes(3);
  });

  it("sem pontos mostra o estado vazio", () => {
    render(<LineChart series={[{ label: "CPU", points: [] }]} stepSeconds={60} format={fmt} />);
    expect(screen.getByText("Sem dados neste período.")).toBeInTheDocument();
  });
});
