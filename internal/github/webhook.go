package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNotBranch indica um push que não é de branch (ex.: tag).
var ErrNotBranch = errors.New("github: o push não é de um branch")

const maxCommitMessage = 200

// Push é o essencial de um evento push.
type Push struct {
	Repo    string
	Branch  string
	After   string
	Message string
	Deleted bool
}

// VerifySignature confere o cabeçalho X-Hub-Signature-256 ("sha256=<hex>")
// contra o HMAC-SHA256 do corpo. Segredo vazio nunca valida.
func VerifySignature(secret, body []byte, header string) bool {
	if len(secret) == 0 {
		return false
	}
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

// ParsePush lê um evento push. Ref que não é refs/heads/<b> → ErrNotBranch.
// Deleted vale quando after é só zeros (branch apagada).
func ParsePush(body []byte) (Push, error) {
	var e struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		HeadCommit *struct {
			Message string `json:"message"`
		} `json:"head_commit"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return Push{}, fmt.Errorf("github: push inválido: %w", err)
	}
	branch, ok := strings.CutPrefix(e.Ref, "refs/heads/")
	if !ok {
		if strings.HasPrefix(e.Ref, "refs/") {
			return Push{}, ErrNotBranch
		}
		return Push{}, errors.New("github: push sem ref de branch")
	}
	if branch == "" {
		return Push{}, errors.New("github: push com branch vazio")
	}
	if e.Repository.FullName == "" {
		return Push{}, errors.New("github: push sem repositório")
	}
	if e.After == "" {
		return Push{}, errors.New("github: push sem commit")
	}
	p := Push{Repo: e.Repository.FullName, Branch: branch, After: e.After,
		Deleted: e.Deleted || strings.Trim(e.After, "0") == ""}
	if e.HeadCommit != nil {
		p.Message = firstLine(e.HeadCommit.Message)
	}
	return p, nil
}

// firstLine devolve a primeira linha, com no máximo maxCommitMessage caracteres.
func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	s = strings.TrimRight(s, "\r")
	if r := []rune(s); len(r) > maxCommitMessage {
		s = string(r[:maxCommitMessage])
	}
	return s
}
