package update_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/update"
)

const assetName = "korego_0.2.0_linux_amd64.tar.gz"

func makeTarball(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		body := []byte("binario-novo")
		if h.Typeflag != tar.TypeReg {
			body = nil
		}
		h.Size = int64(len(body))
		h.Mode = 0o755
		require.NoError(t, tw.WriteHeader(&h))
		_, err := tw.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func releaseServer(t *testing.T, tarball []byte, sum string) update.Release {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dl/" + assetName:
			_, _ = w.Write(tarball)
		case "/dl/checksums.txt":
			fmt.Fprintf(w, "%s  other.tar.gz\n%s  %s\n", "00", sum, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return update.Release{Version: "v0.2.0", TarballURL: srv.URL + "/dl/" + assetName, ChecksumsURL: srv.URL + "/dl/checksums.txt"}
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func newExe(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "korego")
	require.NoError(t, os.WriteFile(p, []byte("binario-antigo"), 0o755))
	return p
}

func TestApply_ReplacesAndKeepsPrev(t *testing.T) {
	tb := makeTarball(t, tar.Header{Name: "korego.service", Typeflag: tar.TypeReg}, tar.Header{Name: "korego", Typeflag: tar.TypeReg})
	rel := releaseServer(t, tb, sha(tb))
	exe := newExe(t)

	require.NoError(t, update.Apply(context.Background(), rel, exe, nil))

	got, _ := os.ReadFile(exe)
	assert.Equal(t, "binario-novo", string(got))
	prev, _ := os.ReadFile(exe + ".prev")
	assert.Equal(t, "binario-antigo", string(prev))
	assert.NoFileExists(t, exe+".new")
}

func TestApply_ChecksumMismatch(t *testing.T) {
	tb := makeTarball(t, tar.Header{Name: "korego", Typeflag: tar.TypeReg})
	rel := releaseServer(t, tb, sha([]byte("outro")))
	exe := newExe(t)

	err := update.Apply(context.Background(), rel, exe, nil)
	require.ErrorIs(t, err, update.ErrChecksum)

	got, _ := os.ReadFile(exe)
	assert.Equal(t, "binario-antigo", string(got))
	assert.NoFileExists(t, exe+".new")
	assert.NoFileExists(t, exe+".prev")
	entries, _ := os.ReadDir(filepath.Dir(exe))
	assert.Len(t, entries, 1, "sem temporários sobrando")
}

func TestApply_RejectsSymlinkAndMissingBinary(t *testing.T) {
	for name, tb := range map[string][]byte{
		"symlink": makeTarball(t, tar.Header{Name: "korego", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"}),
		"ausente": makeTarball(t, tar.Header{Name: "korego.service", Typeflag: tar.TypeReg}),
	} {
		t.Run(name, func(t *testing.T) {
			rel := releaseServer(t, tb, sha(tb))
			exe := newExe(t)
			require.Error(t, update.Apply(context.Background(), rel, exe, nil))
			got, _ := os.ReadFile(exe)
			assert.Equal(t, "binario-antigo", string(got))
			entries, _ := os.ReadDir(filepath.Dir(exe))
			assert.Len(t, entries, 1)
		})
	}
}
