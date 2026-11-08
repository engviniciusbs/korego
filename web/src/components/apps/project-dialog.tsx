"use client";

import { useId, useState, type FormEvent } from "react";
import { Alert, Dialog, Input, Label } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { useSaveProject, type Project } from "@/lib/projects";
import { FieldError, Hint } from "./form-bits";

type Field = "name" | "description";

const MAX_NAME = 100;
const MAX_DESC = 500;

export interface ProjectDialogProps {
  /** Projeto em edição; sem ele, cria um novo. */
  project?: Project;
  onClose: (saved?: Project) => void;
}

/** Criar ou editar projeto (nome e descrição). */
export function ProjectDialog({ project, onClose }: ProjectDialogProps) {
  const uid = useId();
  const formId = `${uid}-form`;
  const errId = (f: Field) => `${uid}-${f}-error`;
  const save = useSaveProject();
  const [name, setName] = useState(project?.name ?? "");
  const [description, setDescription] = useState(project?.description ?? "");
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const [formError, setFormError] = useState("");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (save.isPending) return;
    const n = name.trim();
    const d = description.trim();
    const next: Partial<Record<Field, string>> = {};
    if (!n) next.name = "Informe o nome do projeto.";
    else if ([...n].length > MAX_NAME) next.name = `O nome deve ter até ${MAX_NAME} caracteres.`;
    if ([...d].length > MAX_DESC) next.description = `A descrição deve ter até ${MAX_DESC} caracteres.`;
    setErrors(next);
    setFormError("");
    const first = (["name", "description"] as Field[]).find((f) => next[f]);
    if (first) {
      document.getElementById(`${uid}-${first}`)?.focus();
      return;
    }
    save.mutate(
      { id: project?.id, input: { name: n, description: d } },
      {
        onSuccess: (p) => onClose(p),
        onError: (err) => {
          if (err instanceof ApiError && err.code === "PROJECT_EXISTS") {
            setErrors({ name: "Já existe um projeto com esse nome." });
          } else if (err instanceof ApiError && err.code === "INVALID_PROJECT" && (err.field === "name" || err.field === "description")) {
            setErrors({ [err.field]: err.message });
          } else {
            setFormError(err.message);
          }
        },
      },
    );
  };

  const title = project ? "Editar projeto" : "Novo projeto";

  return (
    <Dialog
      open
      size="lg"
      eyebrow={project ? "Projeto" : "Projetos"}
      title={title}
      confirmLabel={project ? "Salvar" : "Criar projeto"}
      formId={formId}
      loading={save.isPending}
      onCancel={() => onClose()}
    >
      <form id={formId} noValidate onSubmit={submit} className="space-y-4 text-fg">
        {formError && <Alert variant="danger">{formError}</Alert>}
        <div>
          <Label htmlFor={`${uid}-name`} required>
            Nome
          </Label>
          <Input
            id={`${uid}-name`}
            name="name"
            className="mt-1.5"
            value={name}
            maxLength={MAX_NAME}
            placeholder="Loja…"
            autoComplete="off"
            invalid={Boolean(errors.name)}
            aria-describedby={errors.name ? errId("name") : undefined}
            onChange={(e) => {
              setName(e.target.value);
              setErrors((x) => ({ ...x, name: undefined }));
            }}
          />
          <FieldError id={errId("name")}>{errors.name}</FieldError>
        </div>
        <div>
          <Label htmlFor={`${uid}-description`}>Descrição</Label>
          <textarea
            id={`${uid}-description`}
            name="description"
            rows={3}
            value={description}
            maxLength={MAX_DESC}
            placeholder="Para que servem as aplicações deste projeto…"
            aria-invalid={errors.description ? true : undefined}
            aria-describedby={[errors.description ? errId("description") : "", `${uid}-description-hint`].filter(Boolean).join(" ")}
            onChange={(e) => {
              setDescription(e.target.value);
              setErrors((x) => ({ ...x, description: undefined }));
            }}
            className={cn(
              "mt-1.5 block w-full min-w-0 resize-y border border-line-strong bg-surface px-3 py-2 text-[13px] text-fg placeholder:text-fg-subtle",
              "outline-hidden hover:border-fg-subtle focus-visible:border-accent-text focus-visible:bg-surface-2",
              "focus-visible:shadow-[inset_2px_0_0_0_var(--color-accent-text)] aria-invalid:border-danger",
            )}
          />
          <FieldError id={errId("description")}>{errors.description}</FieldError>
          <Hint id={`${uid}-description-hint`}>
            Opcional. <span className="tabular-nums">{[...description].length}</span>/{MAX_DESC}
          </Hint>
        </div>
      </form>
    </Dialog>
  );
}
