// Package deploy busca o código, constrói imagens e publica aplicações.
// Nenhum comando passa por shell: tudo é exec.CommandContext com args separados.
package deploy

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// Cmd é um processo a executar. Env acrescenta variáveis ao ambiente atual;
// segredos (token do GitHub) viajam só por aqui, nunca em Args.
type Cmd struct {
	Name string
	Args []string
	Env  []string
	Dir  string
}

// Runner executa um Cmd, mandando stdout e stderr juntos para out.
type Runner interface {
	Run(ctx context.Context, c Cmd, out io.Writer) error
}

// ExecRunner é o Runner real, baseado em os/exec.
type ExecRunner struct{}

// Run executa o comando; cancelar ctx mata o processo.
func (ExecRunner) Run(ctx context.Context, c Cmd, out io.Writer) error {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	env := append(os.Environ(), c.Env...)
	cmd.Env = append(env, "GIT_TERMINAL_PROMPT=0")
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
