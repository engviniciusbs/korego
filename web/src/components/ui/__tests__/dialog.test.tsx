import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { Dialog } from "@/components/ui/dialog";

function Harness({ onCancel, onConfirm }: { onCancel: () => void; onConfirm: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Abrir
      </button>
      <button type="button">Fora</button>
      <Dialog
        open={open}
        title="Parar container?"
        confirmLabel="Parar"
        variant="danger"
        onConfirm={onConfirm}
        onCancel={() => {
          onCancel();
          setOpen(false);
        }}
      >
        <p>O container web será parado.</p>
      </Dialog>
    </>
  );
}

describe("Dialog", () => {
  it("Dialog: Esc cancela e devolve o foco", () => {
    const onCancel = vi.fn();
    const onConfirm = vi.fn();
    render(<Harness onCancel={onCancel} onConfirm={onConfirm} />);

    const opener = screen.getByRole("button", { name: "Abrir" });
    opener.focus();
    fireEvent.click(opener);

    const dialog = screen.getByRole("dialog", { name: "Parar container?" });
    expect(dialog).toHaveAttribute("open");
    expect(dialog).toHaveAccessibleDescription(/será parado/);

    // Foco inicial na opção menos destrutiva.
    const cancel = screen.getByRole("button", { name: "Cancelar" });
    const confirm = screen.getByRole("button", { name: "Parar" });
    expect(cancel).toHaveFocus();
    expect(confirm).toHaveAttribute("data-variant", "danger");

    // Foco preso: Tab no último volta ao primeiro; Shift+Tab no primeiro vai ao último.
    confirm.focus();
    fireEvent.keyDown(confirm, { key: "Tab" });
    expect(cancel).toHaveFocus();
    fireEvent.keyDown(cancel, { key: "Tab", shiftKey: true });
    expect(confirm).toHaveFocus();

    // Foco que escapou (ex.: clique fora) volta para dentro no próximo Tab.
    screen.getByRole("button", { name: "Fora", hidden: true }).focus();
    fireEvent.keyDown(dialog, { key: "Tab" });
    expect(dialog).toContainElement(document.activeElement as HTMLElement);

    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opener).toHaveFocus();
  });

  it("loading bloqueia o cancelamento e ocupa o botão de confirmar", () => {
    const onCancel = vi.fn();
    render(
      <Dialog open title="Remover?" confirmLabel="Remover" variant="danger" loading onConfirm={() => {}} onCancel={onCancel}>
        corpo
      </Dialog>,
    );
    const dialog = screen.getByRole("dialog", { name: "Remover?" });
    expect(screen.getByRole("button", { name: /Remover/ })).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("button", { name: "Cancelar" })).toBeDisabled();
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(onCancel).not.toHaveBeenCalled();
  });
});
