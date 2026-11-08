package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/engviniciusbs/korego/internal/config"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/store"
)

// dbPath é o caminho do banco dentro do diretório de dados.
func dbPath(dataDir string) string { return filepath.Join(dataDir, "korego.db") }

const painelUsage = "uso: korego painel --porta-publica [--config caminho]\n" +
	"  --porta-publica  reativa a porta pública do painel (recuperação pelo SSH)\n"

// runPainel é o subcomando de recuperação `korego painel`. Devolve o código
// de saída: 0 ok, 1 erro, 2 uso incorreto.
func runPainel(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("painel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, painelUsage) }
	cfgPath := fs.String("config", "/etc/korego/config.toml", "caminho do arquivo de configuração")
	publicPort := fs.Bool("porta-publica", false, "reativa a porta pública do painel")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*publicPort || fs.NArg() > 0 {
		fs.Usage()
		return 2
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "erro: %v\n", err)
		return 1
	}
	path := dbPath(cfg.DataDir)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "erro: banco %s não encontrado; confira data_dir em %s\n", path, *cfgPath)
		} else {
			fmt.Fprintf(stderr, "erro: banco %s: %v\n", path, err)
		}
		return 1
	}
	db, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "erro: %v\n", err)
		return 1
	}
	defer db.Close()
	if err := db.SetSetting(context.Background(), domains.SettingPublicPort, "true"); err != nil {
		fmt.Fprintf(stderr, "erro: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "Porta pública reativada. Reinicie: systemctl restart korego")
	return 0
}
