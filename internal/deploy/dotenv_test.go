package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

const envSample = "# comentário\n\nexport A=1\nB=\"x y\"\nC=a=b\nD=\n"

func TestEnvNames(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(p, []byte(envSample), 0o640))
	got, err := EnvNames(p)
	if err != nil || !reflect.DeepEqual(got, []string{"A", "B", "C", "D"}) {
		t.Fatalf("%v %v", got, err)
	}
}

func TestSetEnv(t *testing.T) {
	linuxOnly(t)
	p := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(p, []byte(envSample), 0o640))
	require.NoError(t, SetEnv(p, map[string]string{"C": "novo", "E": "com espaço"}))
	b, _ := os.ReadFile(p)
	want := "# comentário\n\nexport A=1\nB=\"x y\"\nC=novo\nD=\nE='com espaço'\n"
	if string(b) != want {
		t.Fatalf("%q", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Fatalf("modo %v", fi.Mode())
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Fatalf("sobrou temporário: %v", ents)
	}
}

func TestSetEnv_Aspas(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, SetEnv(p, map[string]string{
		"P": "plain", "W": "p@$$w0rd", "S": "a$b", "Q": `it's $x "y" \z`,
	}))
	b, _ := os.ReadFile(p)
	want := "P=plain\nQ=\"it's $$x \\\"y\\\" \\\\z\"\nS='a$b'\nW='p@$$w0rd'\n"
	require.Equal(t, want, string(b))
}

func TestQuoteEnv(t *testing.T) {
	for in, want := range map[string]string{
		"abc123":            "abc123",
		"":                  "",
		"https://x.io/a?b":  "'https://x.io/a?b'",
		"a;id>/tmp/x":       "'a;id>/tmp/x'",
		"x`id`":             "'x`id`'",
		"$(id)":             "'$(id)'",
		"a|b&c":             "'a|b&c'",
		"it's $x":           `"it's $$x"`,
		"it's `id`":         "\"it's \\`id\\`\"",
		"user@host:5432/db": "user@host:5432/db",
	} {
		require.Equal(t, want, quoteEnv(in), in)
	}
}

func TestEnvValues(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(p, []byte("# X=comentado\nexport A=plain\nB='simples $x'\n"+
		"C=\"it's $$y\"\nD=\nE=valor # nota\nruim linha\n"), 0o600))
	got, err := EnvValues(p)
	require.NoError(t, err)
	require.Equal(t, []string{"plain", "simples $x", "'simples $x'", "it's $y", `"it's $$y"`, "valor", "valor # nota"}, got)
	_, err = EnvValues(filepath.Join(t.TempDir(), ".env"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSetEnv_SeguraLink(t *testing.T) {
	d := t.TempDir()
	real := filepath.Join(d, "shared.env")
	require.NoError(t, os.WriteFile(real, []byte("A=1\n"), 0o640))
	link := filepath.Join(d, ".env")
	require.NoError(t, os.Symlink(real, link))
	require.NoError(t, SetEnv(link, map[string]string{"A": "2"}))
	fi, err := os.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&os.ModeSymlink, "o link foi substituído")
	b, _ := os.ReadFile(real)
	require.Equal(t, "A=2\n", string(b))
}

func TestSetEnv_ExportEArquivoSemQuebraFinal(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(p, []byte("export A=1\nB=2"), 0o600))
	require.NoError(t, SetEnv(p, map[string]string{"A": "9", "N": "1"}))
	b, _ := os.ReadFile(p)
	if string(b) != "export A=9\nB=2\nN=1\n" {
		t.Fatalf("%q", b)
	}
}

func TestSetEnv_ArquivoAusente(t *testing.T) {
	linuxOnly(t)
	p := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, SetEnv(p, map[string]string{"A": "1"}))
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
}
