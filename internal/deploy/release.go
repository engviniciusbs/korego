package deploy

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// WWWRoot é a raiz onde ficam os sites estáticos publicados.
const WWWRoot = "/var/www"

// wwwRoot pode ser sobrescrita nos testes.
var wwwRoot = WWWRoot

var releaseNameRe = regexp.MustCompile(`^\d{14}-[0-9a-f]{7}$`)

// ReleaseName monta o nome da versão: <UTC YYYYMMDDHHMMSS>-<sha7>.
func ReleaseName(t time.Time, sha string) string {
	if len(sha) > 7 {
		sha = sha[:7]
	}
	if sha == "" {
		sha = "0000000"
	}
	return t.UTC().Format("20060102150405") + "-" + sha
}

// DetectBuildCommand escolhe o comando de build pelo lockfile.
// Sem package.json devolve "" (sem build).
func DetectBuildCommand(dir string) string {
	has := func(n string) bool {
		_, err := os.Stat(filepath.Join(dir, n))
		return err == nil
	}
	if !has("package.json") {
		return ""
	}
	switch {
	case has("pnpm-lock.yaml"):
		return "corepack enable && pnpm install --frozen-lockfile && pnpm run build"
	case has("yarn.lock"):
		return "corepack enable && yarn install --frozen-lockfile && yarn build"
	default:
		return "npm ci && npm run build"
	}
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// InsideRoot resolve links dos dois caminhos e exige que path fique dentro de
// root. Devolve o caminho resolvido.
func InsideRoot(root, path string) (string, error) {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("raiz inválida: %w", err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("caminho inválido: %w", err)
	}
	r, p = filepath.Clean(r), filepath.Clean(p)
	if !within(r, p) {
		return "", fmt.Errorf("o caminho %s está fora da raiz %s", p, r)
	}
	return p, nil
}

// PublishRelease copia srcDir para <publishRoot>/releases/<name>, normaliza as
// permissões e confere o .html da raiz. Em erro apaga a pasta parcial.
func PublishRelease(srcDir, publishRoot, name string, spa bool) (string, error) {
	if !releaseNameRe.MatchString(name) {
		return "", fmt.Errorf("nome de versão inválido: %q", name)
	}
	srcReal, err := filepath.EvalSymlinks(srcDir)
	if err != nil {
		return "", fmt.Errorf("pasta de saída não encontrada: %w", err)
	}
	dest, err := filepath.Abs(filepath.Join(publishRoot, "releases", name))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.Mkdir(dest, 0o755); err != nil {
		return "", err
	}
	if err := copyTree(srcReal, dest); err != nil {
		os.RemoveAll(dest)
		return "", err
	}
	if err := checkHTML(dest, spa); err != nil {
		os.RemoveAll(dest)
		return "", err
	}
	return dest, nil
}

func checkHTML(dir string, spa bool) error {
	if spa {
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
			return errors.New("a saída do build não tem index.html na raiz")
		}
		return nil
	}
	m, _ := filepath.Glob(filepath.Join(dir, "*.html"))
	if len(m) == 0 {
		return errors.New("a saída do build não tem nenhum .html na raiz")
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				return fmt.Errorf("link quebrado %s: %w", rel, err)
			}
			if !within(src, real) {
				return fmt.Errorf("link simbólico %s sai da pasta publicada", rel)
			}
			fi, err := os.Stat(real)
			if err != nil {
				return err
			}
			if fi.IsDir() {
				return fmt.Errorf("link simbólico para pasta não é suportado: %s", rel)
			}
			return copyFile(real, target)
		}
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			return os.Mkdir(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("tipo de arquivo não suportado: %s", rel)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Dono root vem do processo do KoreGO (roda como root); só normaliza o modo.
	return os.Chmod(dst, 0o644)
}

// SwapCurrent troca o link current de forma atômica (symlink tmp + rename).
func SwapCurrent(publishRoot, releaseDir string) error {
	tmp := filepath.Join(publishRoot, "current.tmp")
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(releaseDir, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(publishRoot, "current")); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// CurrentRelease devolve o destino resolvido de current.
func CurrentRelease(publishRoot string) (string, error) {
	return filepath.EvalSymlinks(filepath.Join(publishRoot, "current"))
}

// PruneReleases apaga versões antigas (só pastas no padrão de nome), mantendo
// as keep mais recentes, as protegidas e a que current aponta.
func PruneReleases(publishRoot string, keep int, protect ...string) error {
	dir := filepath.Join(publishRoot, "releases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	norm := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	prot := map[string]bool{}
	for _, p := range protect {
		if p != "" {
			prot[norm(p)] = true
		}
	}
	if _, lerr := os.Lstat(filepath.Join(publishRoot, "current")); lerr == nil {
		cur, err := CurrentRelease(publishRoot)
		switch {
		case err == nil:
			prot[filepath.Clean(cur)] = true
		case !os.IsNotExist(err):
			return fmt.Errorf("não foi possível resolver current: %w", err)
		}
	} else if !os.IsNotExist(lerr) {
		return lerr
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && releaseNameRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if keep < 0 {
		keep = 0
	}
	var firstErr error
	for i, n := range names {
		if i < keep {
			continue
		}
		p := filepath.Join(dir, n)
		if prot[norm(p)] {
			continue
		}
		if err := os.RemoveAll(p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ReleaseSHA devolve o sha7 do nome de uma versão no padrão
// <YYYYMMDDHHMMSS>-<sha7>, ou "" se o nome não segue o padrão.
func ReleaseSHA(name string) string {
	if !releaseNameRe.MatchString(name) {
		return ""
	}
	return name[len(name)-7:]
}

// ValidateAdoptRoot confere o layout de adoção: current é um link para
// <raiz>/releases/<nome>, e a raiz fica dentro de wwwRoot.
func ValidateAdoptRoot(current string) (publishRoot, releaseDir string, err error) {
	return ValidateAdoptRootIn(wwwRoot, current)
}

// ValidateAdoptRootIn é ValidateAdoptRoot com a raiz dos sites explícita
// ("" = WWWRoot).
func ValidateAdoptRootIn(wwwRoot, current string) (publishRoot, releaseDir string, err error) {
	if wwwRoot == "" {
		wwwRoot = WWWRoot
	}
	if !filepath.IsAbs(current) {
		return "", "", errors.New("current precisa ser um caminho absoluto")
	}
	current = filepath.Clean(current)
	fi, err := os.Lstat(current)
	if err != nil {
		return "", "", fmt.Errorf("current não encontrado: %w", err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return "", "", errors.New("current precisa ser um link simbólico, não uma pasta")
	}
	root, err := InsideRoot(wwwRoot, filepath.Dir(current))
	if err != nil {
		return "", "", err
	}
	if wr, werr := filepath.EvalSymlinks(wwwRoot); werr != nil || filepath.Clean(wr) == root {
		return "", "", errors.New("a raiz de publicação precisa ser uma subpasta de " + wwwRoot)
	}
	rel, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", "", fmt.Errorf("o link current está quebrado: %w", err)
	}
	rel = filepath.Clean(rel)
	if filepath.Dir(rel) != filepath.Join(root, "releases") {
		return "", "", errors.New("current precisa apontar para <raiz>/releases/<versão>")
	}
	return root, rel, nil
}
