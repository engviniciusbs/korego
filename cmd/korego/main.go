// Comando korego: gerenciador de VPS self-hosted em um único binário.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/config"
	"github.com/engviniciusbs/korego/internal/version"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		if err := serve(args); err != nil {
			log.Fatalf("erro: %v", err)
		}
	case "version":
		fmt.Println(version.Version)
	default:
		fmt.Fprintf(os.Stderr, "comando desconhecido: %s\nuso: korego [serve|version]\n", cmd)
		os.Exit(2)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/korego/config.toml", "caminho do arquivo de configuração")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.NewRouter(api.Deps{}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Printf("KoreGO %s ouvindo em %s", version.Version, cfg.ListenAddr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
