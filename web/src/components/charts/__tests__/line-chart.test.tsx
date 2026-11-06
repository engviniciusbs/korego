import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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

  it("sem pontos mostra o estado vazio", () => {
    render(<LineChart series={[{ label: "CPU", points: [] }]} stepSeconds={60} format={fmt} />);
    expect(screen.getByText("Sem dados neste período.")).toBeInTheDocument();
  });
});
