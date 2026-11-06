import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Button } from "@/components/ui/button";

describe("Button", () => {
  it("renderiza variantes e fica desabilitado em loading", () => {
    const { rerender } = render(<Button variant="primary">Salvar</Button>);
    const btn = screen.getByRole("button", { name: "Salvar" });
    expect(btn).toHaveAttribute("data-variant", "primary");
    expect(btn).toHaveAttribute("type", "button");
    expect(btn).toBeEnabled();
    expect(btn).not.toHaveAttribute("aria-busy");

    for (const variant of ["secondary", "ghost", "danger"] as const) {
      rerender(<Button variant={variant}>Salvar</Button>);
      expect(screen.getByRole("button", { name: "Salvar" })).toHaveAttribute(
        "data-variant",
        variant,
      );
    }

    rerender(
      <Button variant="primary" loading>
        Salvar
      </Button>,
    );
    const busy = screen.getByRole("button", { name: /Salvar/ });
    expect(busy).toHaveAttribute("aria-busy", "true");
    expect(busy).toBeDisabled();
  });

  it("aplica tamanho e nunca usa cantos arredondados", () => {
    render(<Button size="sm">Ok</Button>);
    const btn = screen.getByRole("button", { name: "Ok" });
    expect(btn).toHaveAttribute("data-size", "sm");
    expect(btn.className).not.toMatch(/\brounded-(?!none\b)/);
  });
});
