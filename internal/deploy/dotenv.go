package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// envKey extrai a chave de uma linha de .env (aceita "export K=V").
func envKey(line string) (key string, ok bool) {
	t := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(t, "#") {
		return "", false
	}
	if strings.HasPrefix(t, "export ") || strings.HasPrefix(t, "export\t") {
		t = strings.TrimLeft(t[7:], " \t")
	}
	i := strings.IndexByte(t, '=')
	if i <= 0 {
		return "", false
	}
	k := strings.TrimRight(t[:i], " \t")
	if !ValidEnvName(k) {
		return "", false
	}
	return k, true
}

// EnvNames devolve os nomes das variáveis do arquivo, na ordem. Nunca os valores.
func EnvNames(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, l := range strings.Split(string(b), "\n") {
		if k, ok := envKey(strings.TrimRight(l, "\r")); ok {
			names = append(names, k)
		}
	}
	return names, nil
}

// quoteEnv protege o valor para o compose: aspas simples (sem expansão) quando
// possível; com ' no valor, aspas duplas com \ e " escapados e $ escrito $$.
func quoteEnv(v string) string {
	if !strings.ContainsAny(v, " \t#\"'$") {
		return v
	}
	if !strings.Contains(v, "'") {
		return "'" + v + "'"
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, `$`, `$$`)
	return `"` + v + `"`
}

// SetEnv altera ou acrescenta variáveis, mantendo as demais linhas byte a byte.
// Grava via arquivo temporário + rename, preservando o modo (0600 se novo).
func SetEnv(path string, set map[string]string) error {
	for k, v := range set {
		if !ValidEnvName(k) {
			return fmt.Errorf("nome de variável inválido: %q", k)
		}
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("o valor de %s não pode ter quebra de linha", k)
		}
	}
	if r, e := filepath.EvalSymlinks(path); e == nil {
		path = r
	}
	mode := os.FileMode(0o600)
	var orig os.FileInfo
	var content string
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		content = string(b)
		if fi, e := os.Stat(path); e == nil {
			mode = fi.Mode().Perm()
			orig = fi
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return err
	}

	lines := strings.Split(content, "\n")
	done := map[string]bool{}
	for i, l := range lines {
		k, ok := envKey(strings.TrimRight(l, "\r"))
		if !ok {
			continue
		}
		v, want := set[k]
		if !want {
			continue
		}
		t := strings.TrimLeft(l, " \t")
		prefix := l[:len(l)-len(t)]
		if strings.HasPrefix(t, "export ") || strings.HasPrefix(t, "export\t") {
			rest := t[7:]
			trimmed := strings.TrimLeft(rest, " \t")
			prefix += t[:7] + rest[:len(rest)-len(trimmed)]
		}
		eol := ""
		if strings.HasSuffix(l, "\r") {
			eol = "\r"
		}
		lines[i] = prefix + k + "=" + quoteEnv(v) + eol
		done[k] = true
	}
	out := strings.Join(lines, "\n")
	var fresh []string
	for k := range set {
		if !done[k] {
			fresh = append(fresh, k)
		}
	}
	sort.Strings(fresh)
	for _, k := range fresh {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += k + "=" + quoteEnv(set[k]) + "\n"
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".env-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(e error) error { tmp.Close(); os.Remove(name); return e }
	if _, err := tmp.WriteString(out); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if orig != nil {
		chownLike(name, orig)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
