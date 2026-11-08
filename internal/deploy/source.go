package deploy

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/store"
)

// Source descreve de onde buscar o código. Token (opcional) é o token de
// instalação do GitHub e só viaja por variável de ambiente do processo git.
type Source struct {
	URL, Branch, Token string
}

// Fetch faz um clone raso do branch em dir e devolve o SHA e o assunto do commit.
func Fetch(ctx context.Context, r Runner, dir string, src Source, out io.Writer) (sha, msg string, err error) {
	return fetch(ctx, r, dir, src, out, false)
}

// fetch é o Fetch; allowFile aceita também file:// (só testes de integração).
func fetch(ctx context.Context, r Runner, dir string, src Source, out io.Writer, allowFile bool) (sha, msg string, err error) {
	if !ValidGitURL(src.URL) && !(allowFile && validFileURL(src.URL)) {
		return "", "", errors.New("URL do repositório inválida")
	}
	if !ValidBranch(src.Branch) {
		return "", "", errors.New("branch inválido")
	}
	clone := Cmd{Name: "git", Args: []string{"clone", "--depth", "1", "--branch", src.Branch, "--single-branch", src.URL, dir}}
	clone.Env = tokenEnv(src.Token)
	if err := r.Run(ctx, clone, out); err != nil {
		return "", "", fmt.Errorf("git clone: %w", err)
	}
	return headInfo(ctx, r, dir)
}

// tokenEnv devolve as variáveis que mandam o token só para github.com, ou nil.
func tokenEnv(token string) []string {
	if token == "" {
		return nil
	}
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + tokenCred(token),
	}
}

// AppDir é o diretório de trabalho persistente de uma aplicação Compose
// (<dataDir>/apps/<slug>). Apagar a aplicação apaga esse diretório.
func AppDir(dataDir, slug string) string {
	return filepath.Join(dataDir, "apps", slug)
}

// FetchInto atualiza um diretório de trabalho persistente: sem repositório em
// dir, faz o clone raso; com repositório, aponta origin para a URL, busca o
// branch (raso) e faz reset --hard FETCH_HEAD. Nunca roda git clean: arquivos
// não versionados e ignorados (dados de bind mounts) sobrevivem.
func FetchInto(ctx context.Context, r Runner, dir string, src Source, out io.Writer) (sha, msg string, err error) {
	return fetchInto(ctx, r, dir, src, out, false)
}

func fetchInto(ctx context.Context, r Runner, dir string, src Source, out io.Writer, allowFile bool) (sha, msg string, err error) {
	if _, serr := os.Stat(filepath.Join(dir, ".git")); serr != nil {
		return fetch(ctx, r, dir, src, out, allowFile)
	}
	if !ValidGitURL(src.URL) && !(allowFile && validFileURL(src.URL)) {
		return "", "", errors.New("URL do repositório inválida")
	}
	if !ValidBranch(src.Branch) {
		return "", "", errors.New("branch inválido")
	}
	if err := r.Run(ctx, Cmd{Name: "git", Args: []string{"-C", dir, "remote", "set-url", "origin", src.URL}}, out); err != nil {
		return "", "", fmt.Errorf("git remote set-url: %w", err)
	}
	f := Cmd{Name: "git", Args: []string{"-C", dir, "fetch", "--depth", "1", "origin", src.Branch}, Env: tokenEnv(src.Token)}
	if err := r.Run(ctx, f, out); err != nil {
		return "", "", fmt.Errorf("git fetch: %w", err)
	}
	if err := r.Run(ctx, Cmd{Name: "git", Args: []string{"-C", dir, "reset", "--hard", "FETCH_HEAD"}}, out); err != nil {
		return "", "", fmt.Errorf("git reset: %w", err)
	}
	return headInfo(ctx, r, dir)
}

// headInfo devolve o SHA e o assunto do commit em HEAD.
func headInfo(ctx context.Context, r Runner, dir string) (sha, msg string, err error) {
	if sha, err = capture(ctx, r, Cmd{Name: "git", Args: []string{"-C", dir, "rev-parse", "HEAD"}}); err != nil {
		return "", "", fmt.Errorf("git rev-parse: %w", err)
	}
	if msg, err = capture(ctx, r, Cmd{Name: "git", Args: []string{"-C", dir, "log", "-1", "--format=%s"}}); err != nil {
		return "", "", fmt.Errorf("git log: %w", err)
	}
	return sha, msg, nil
}

// validFileURL aceita file:///caminho sem espaços nem caracteres de controle.
func validFileURL(s string) bool {
	return strings.HasPrefix(s, "file:///") && !strings.ContainsAny(s, " \t\r\n\x00")
}

func tokenCred(token string) string {
	return base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
}

// TokenSecrets devolve o que o Redactor deve mascarar para um token do GitHub:
// o token e a credencial base64 enviada no cabeçalho.
func TokenSecrets(token string) []string {
	if token == "" {
		return nil
	}
	return []string{token, tokenCred(token)}
}

// capture roda c e devolve a saída sem espaços nas pontas.
func capture(ctx context.Context, r Runner, c Cmd) (string, error) {
	var b bytes.Buffer
	if err := r.Run(ctx, c, &b); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

// ImageForApp devolve a tag da imagem da aplicação: korego/<slug>:<7 primeiros do sha>.
// Sem sha (origem image), usa os 7 primeiros hex do ID da imagem puxada.
func ImageForApp(app store.App, sha, imageID string) string {
	short := sha
	if short == "" {
		short = strings.TrimPrefix(imageID, "sha256:")
	}
	if len(short) > 7 {
		short = short[:7]
	}
	return "korego/" + app.Slug + ":" + short
}

// PullImage puxa ref pelo Docker e devolve o ID da imagem ("sha256:...").
// Escolha: o ID vem de `docker image inspect --format {{.Id}}` pelo Runner,
// sem ampliar a interface docker.Client; serve para montar a tag com ImageForApp.
func PullImage(ctx context.Context, d docker.Client, r Runner, ref string, out io.Writer) (string, error) {
	if !ValidImageRef(ref) {
		return "", errors.New("referência de imagem inválida")
	}
	err := d.Pull(ctx, ref, func(line string) { _, _ = io.WriteString(out, line+"\n") })
	if err != nil {
		return "", fmt.Errorf("pull %s: %w", ref, err)
	}
	id, err := capture(ctx, r, Cmd{Name: "docker", Args: []string{"image", "inspect", "--format", "{{.Id}}", ref}})
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", ref, err)
	}
	return id, nil
}

// PullAndTag puxa ref e a etiqueta como tag. Quem precisa do ID para montar a
// tag chama PullImage, ImageForApp e depois TagImage.
func PullAndTag(ctx context.Context, d docker.Client, r Runner, ref, tag string, out io.Writer) error {
	if !ValidImageRef(ref) {
		return errors.New("referência de imagem inválida")
	}
	err := d.Pull(ctx, ref, func(line string) { _, _ = io.WriteString(out, line+"\n") })
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	return TagImage(ctx, r, ref, tag, out)
}

// TagImage roda `docker tag <ref> <tag>`.
func TagImage(ctx context.Context, r Runner, ref, tag string, out io.Writer) error {
	if err := r.Run(ctx, Cmd{Name: "docker", Args: []string{"tag", ref, tag}}, out); err != nil {
		return fmt.Errorf("docker tag: %w", err)
	}
	return nil
}
