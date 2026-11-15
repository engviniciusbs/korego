package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const envSample = "# comentário\n\nexport A=1\nB=\"x y\"\nC=a=b\nD=\n"

func TestEnvNames(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte(envSample), 0o640)
	got, err := EnvNames(p)
	if err != nil || !reflect.DeepEqual(got, []string{"A", "B", "C", "D"}) {
		t.Fatalf("%v %v", got, err)
	}
}

func TestSetEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte(envSample), 0o640)
	if err := SetEnv(p, map[string]string{"C": "novo", "E": "com espaço"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "# comentário\n\nexport A=1\nB=\"x y\"\nC=novo\nD=\nE=\"com espaço\"\n"
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

func TestSetEnv_Escapa(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := SetEnv(p, map[string]string{"Q": `a "b" \c $d`, "P": "plain"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "P=plain\nQ=\"a \\\"b\\\" \\\\c $d\"\n" {
		t.Fatalf("%q", b)
	}
}

func TestSetEnv_ExportEArquivoSemQuebraFinal(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("export A=1\nB=2"), 0o600)
	SetEnv(p, map[string]string{"A": "9", "N": "1"})
	b, _ := os.ReadFile(p)
	if string(b) != "export A=9\nB=2\nN=1\n" {
		t.Fatalf("%q", b)
	}
}

func TestSetEnv_ArquivoAusente(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := SetEnv(p, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
}
