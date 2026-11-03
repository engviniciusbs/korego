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
	"path/filepath"
	"syscall"
	"time"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/config"
	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
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

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("criar diretório de dados %s: %w", cfg.DataDir, err)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "korego.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	users, err := db.CountUsers(context.Background())
	if err != nil {
		return fmt.Errorf("contar usuários: %w", err)
	}
	keyPath := filepath.Join(cfg.ConfigDir, "master.key")
	key, err := secrets.LoadOrCreateKey(keyPath, users == 0)
	if errors.Is(err, secrets.ErrKeyMissing) {
		return fmt.Errorf("a chave mestra %s não existe, mas o banco já possui usuários; "+
			"restaure o arquivo original da chave (sem ela os segredos não podem ser decifrados)", keyPath)
	}
	if err != nil {
		return err
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		return err
	}

	tokenPath := filepath.Join(cfg.ConfigDir, "setup-token")
	var setupToken string
	if users == 0 {
		setupToken, err = loadOrCreateSetupToken(tokenPath)
		if err != nil {
			return err
		}
		log.Printf("Setup pendente. Token de setup: %s (também em %s)", setupToken, tokenPath)
	} else if err := os.Remove(tokenPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("remover token de setup antigo %s: %v", tokenPath, err)
	}

	svc := &auth.Service{
		DB:             db,
		Box:            box,
		Sessions:       &auth.Sessions{DB: db},
		Limiter:        auth.NewLimiter(5, time.Minute, 15*time.Minute),
		Audit:          &audit.Logger{DB: db},
		SetupToken:     setupToken,
		SetupTokenFile: tokenPath,
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.NewRouter(api.Deps{Auth: svc, Node: node.NewLocal()}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		cleanupSessions(ctx, db)
	}()
	defer func() { stop(); <-cleanupDone }()

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

// cleanupSessions removes expired sessions every hour until ctx is canceled.
func cleanupSessions(ctx context.Context, db *store.DB) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := db.DeleteExpiredSessions(ctx, time.Now()); err != nil && ctx.Err() == nil {
				log.Printf("limpar sessões expiradas: %v", err)
			}
		}
	}
}
