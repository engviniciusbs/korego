package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	checksumsName   = "checksums.txt"
	maxTarballSize  = 200 << 20
	maxChecksumSize = 1 << 20
	maxBinarySize   = 200 << 20
)

// ErrChecksum indica que o SHA-256 do tarball não confere com checksums.txt.
var ErrChecksum = errors.New("checksum do download não confere")

// Apply baixa e verifica a release e troca o binário em exePath, mantendo o
// anterior em exePath+".prev". Em qualquer erro nada é trocado e os arquivos
// temporários são removidos.
func Apply(ctx context.Context, rel Release, exePath string, httpc *http.Client) (err error) {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	dir := filepath.Dir(exePath)
	newPath := exePath + ".new"

	tmp, err := os.CreateTemp(dir, ".korego-dl-*")
	if err != nil {
		return fmt.Errorf("criar arquivo temporário: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		if err != nil {
			_ = os.Remove(newPath)
		}
	}()

	sum, err := download(ctx, httpc, rel.TarballURL, tmp, maxTarballSize)
	if err != nil {
		return fmt.Errorf("baixar tarball: %w", err)
	}

	var sums strings.Builder
	if _, err = download(ctx, httpc, rel.ChecksumsURL, &sums, maxChecksumSize); err != nil {
		return fmt.Errorf("baixar checksums: %w", err)
	}
	asset := assetFileName(rel.TarballURL)
	want, ok := findChecksum(sums.String(), asset)
	if !ok {
		return fmt.Errorf("%w: %s ausente em %s", ErrChecksum, asset, checksumsName)
	}
	if sum != want {
		return ErrChecksum
	}

	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err = extractBinary(tmp, newPath); err != nil {
		return err
	}

	if err = os.Remove(exePath + ".prev"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remover .prev antigo: %w", err)
	}
	if err = os.Rename(exePath, exePath+".prev"); err != nil {
		return fmt.Errorf("guardar binário atual: %w", err)
	}
	if err = os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(exePath+".prev", exePath)
		return fmt.Errorf("instalar novo binário: %w", err)
	}
	return nil
}

// download copia a resposta de u para w, limitada a limit bytes, e devolve o
// SHA-256 hexadecimal do conteúdo.
func download(ctx context.Context, httpc *http.Client, u string, w io.Writer, limit int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("download excede %d bytes", limit)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func assetFileName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return path.Base(rawURL)
	}
	return path.Base(u.Path)
}

// findChecksum procura a linha "<hex>  <arquivo>" do arquivo exato.
func findChecksum(sums, file string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == file {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// extractBinary grava a entrada regular "korego" do tarball em dst (0755).
func extractBinary(r io.Reader, dst string) (err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("abrir tarball: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, rerr := tr.Next()
		if errors.Is(rerr, io.EOF) {
			return errors.New("binário korego não encontrado no tarball")
		}
		if rerr != nil {
			return fmt.Errorf("ler tarball: %w", rerr)
		}
		if h.Name != "korego" && h.Name != "./korego" {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return errors.New("entrada korego do tarball não é um arquivo regular")
		}
		f, oerr := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if oerr != nil {
			return fmt.Errorf("criar %s: %w", dst, oerr)
		}
		defer func() {
			if cerr := f.Close(); err == nil && cerr != nil {
				err = cerr
			}
			if err != nil {
				_ = os.Remove(dst)
			}
		}()
		n, cerr := io.Copy(f, io.LimitReader(tr, maxBinarySize+1))
		if cerr != nil {
			return fmt.Errorf("extrair binário: %w", cerr)
		}
		if n > maxBinarySize {
			return errors.New("binário excede o tamanho máximo")
		}
		if err = f.Sync(); err != nil {
			return err
		}
		return f.Chmod(0o755)
	}
}
