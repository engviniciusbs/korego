// Comando korego: gerenciador de VPS self-hosted em um único binário.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/cloudflare"
	"github.com/engviniciusbs/korego/internal/config"
	"github.com/engviniciusbs/korego/internal/deploy"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/github"
	"github.com/engviniciusbs/korego/internal/jobs"
	"github.com/engviniciusbs/korego/internal/migrate"
	"github.com/engviniciusbs/korego/internal/monitor"
	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/serve"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/engviniciusbs/korego/internal/update"
	"github.com/engviniciusbs/korego/internal/usage"
	"github.com/engviniciusbs/korego/internal/version"
	"github.com/engviniciusbs/korego/internal/webui"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		if err := runServe(args); err != nil {
			log.Fatalf("erro: %v", err)
		}
	case "version":
		fmt.Println(version.Version)
	case "update-finish":
		if err := updateFinish(args); err != nil {
			log.Fatalf("erro: %v", err)
		}
	case "painel":
		os.Exit(runPainel(args, os.Stdout, os.Stderr))
	default:
		fmt.Fprintf(os.Stderr, "comando desconhecido: %s\nuso: korego [serve|version|update-finish|painel]\n", cmd)
		os.Exit(2)
	}
}

func runServe(args []string) error {
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
	db, err := store.Open(dbPath(cfg.DataDir))
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

	nd := node.NewLocal(cfg.PM2Path, cfg.PM2Home)
	hub := events.NewHub()
	hub.RegisterSource("system", api.SystemSource(nd, 5*time.Second))
	hub.RegisterSource("containers", monitor.ContainerEventsSource(nd.Docker(), 5*time.Second))
	hub.RegisterSource("container-logs", monitor.ContainerLogsSource(nd.Docker(), 2*time.Second))
	hub.RegisterSource("container-stats", monitor.ContainerStatsSource(nd.Docker(), 2*time.Second))

	collector := &monitor.Collector{Source: nd.Metrics(), Hub: hub, DB: db}
	usageCollector := &usage.Collector{Procs: nd.Procs(), Docker: nd.Docker(), PM2: nd.PM2(), Hub: hub, DB: db}

	var web http.Handler
	if cfg.DevProxy != "" {
		web, err = webui.NewDevProxy(cfg.DevProxy)
		if err != nil {
			return err
		}
		log.Printf("Modo dev: frontend via proxy para %s", cfg.DevProxy)
	} else {
		web = webui.NewHandler(webui.Dist())
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("localizar executável: %w", err)
	}
	// Endereço efetivo: com a porta pública desligada, só 127.0.0.1:<porta>
	// (o painel fica acessível pelo domínio, via proxy).
	publicPort, err := domains.PublicPort(context.Background(), db)
	if err != nil {
		return err
	}
	listenAddr, err := domains.EffectiveListenAddr(cfg.ListenAddr, publicPort)
	if err != nil {
		return err
	}
	panelPort, err := domains.ListenPort(cfg.ListenAddr)
	if err != nil {
		return err
	}
	listener, err := serve.NewListener(listenAddr)
	if err != nil {
		return fmt.Errorf("escutar em %s: %w", listenAddr, err)
	}
	defer listener.Close()

	certSvc := &certs.Service{DB: db, Box: box}
	// Faixas da Cloudflare: lista gravada (ou embutida) já na subida; Run
	// atualiza a cada 24 h, mais abaixo.
	cfRanges := cloudflare.New(db, nil)

	// Modo do proxy gravado no banco: o ensaio sobrevive a reinícios e a
	// quedas breves do nginx; só a troca sai dele.
	if c, ok := nd.Proxy().(*proxy.Caddy); ok {
		c.Settings = db
		c.UnitFor = migrate.UnitForProcess
	}

	domSvc := &domains.Service{
		DB: db, Proxy: nd.Proxy(), Hub: hub, PublicIP: cfg.PublicIP,
		PanelPort: panelPort, ListenAddr: cfg.ListenAddr,
		OnListenChange: listener.Switch,
		Certs:          certSvc, Ranges: cfRanges,
	}

	// Troca nginx/apache2 → Caddy e volta (systemctl só no Linux).
	migEngine := &migrate.Engine{
		Domains: domSvc, Proxy: nd.Proxy(), Systemd: migrate.Systemd{}, DB: db, Hub: hub,
	}

	queue := jobs.NewQueue(cfg.MaxBuilds)
	// Declarado depois do defer db.Close, roda antes dele: cancela os deploys
	// em andamento e espera terminarem. Os que esperavam ficam queued e voltam
	// à fila no Recover da próxima subida.
	defer queue.Close()
	ghSvc := &github.Service{DB: db, Box: box, Client: &github.Client{APIURL: cfg.GitHubAPIURL, WebURL: cfg.GitHubWebURL}}
	// Tokens antes do Recover: os deploys github reenfileirados pedem o token ao rodar.
	deploySvc := &deploy.Service{
		DB: db, Docker: nd.Docker(), Runner: deploy.ExecRunner{}, Box: box, Hub: hub,
		Queue: queue, Router: domSvc, Tokens: ghSvc,
		DataDir: cfg.DataDir,
	}
	// Antes de reaplicar as rotas e de servir: marca interrompidos, remove
	// containers órfãos e reenfileira os queued.
	if err := deploySvc.Recover(context.Background()); err != nil {
		log.Printf("deploy: recuperar deploys na subida: %v", err)
	}

	handler := api.NewRouter(api.Deps{
		Auth: svc, Node: nd, Hub: hub, Web: web, Metrics: collector, Usage: usageCollector, DB: db,
		Updater: &update.System{ExePath: exe, ConfigPath: *cfgPath},
		Domains: domSvc, Migrate: migEngine, Certs: certSvc, Deploys: deploySvc, DataDir: cfg.DataDir, Runner: deploy.ExecRunner{}, Box: box,
		GitHub:          ghSvc,
		GitHubConnected: ghSvc.Ready,
	})
	if cfg.DevProxy == "" {
		handler = api.SecurityHeaders(handler, cfg.GitHubWebURL)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Conexões keep-alive abertas pela porta pública terminam em tempo
		// limitado depois que ela é desligada.
		IdleTimeout: 120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	domainsDone := make(chan struct{})
	go func() {
		defer close(domainsDone)
		// Troca do proxy interrompida por queda ou reinício: registra como
		// falha e oferece Voltar se o Caddy ficou nas portas.
		if err := migEngine.RecoverInterrupted(ctx); err != nil {
			log.Printf("proxy: recuperar troca interrompida: %v", err)
		}
		domSvc.Run(ctx)
	}()
	defer func() { stop(); <-domainsDone }()
	// Reaplica as rotas na subida (o proxy pode ter sido reiniciado).
	go func() {
		if err := domSvc.Reapply(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, proxy.ErrInactive) {
			log.Printf("proxy: reaplicar rotas na subida: %v", err)
		}
	}()

	// Faixas da Cloudflare: atualiza na subida e a cada 24 h.
	cfDone := make(chan struct{})
	go func() {
		defer close(cfDone)
		cfRanges.Run(ctx)
	}()
	defer func() { stop(); <-cfDone }()

	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		cleanupSessions(ctx, db)
	}()
	defer func() { stop(); <-cleanupDone }()

	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		collector.Run(ctx)
	}()
	// Declarado depois do defer anterior, roda antes: espera o minuto parcial
	// ser gravado antes de db.Close.
	defer func() { stop(); <-collectorDone }()

	usageDone := make(chan struct{})
	go func() {
		defer close(usageDone)
		usageCollector.Run(ctx)
	}()
	// Idem para o consumo por dono: o minuto parcial vai ao banco antes de db.Close.
	defer func() { stop(); <-usageDone }()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()
	log.Printf("KoreGO %s ouvindo em %s", version.Version, listenAddr)

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

// healthURL deriva a URL de /api/health a partir do endereço de escuta:
// host vazio, 0.0.0.0 ou :: viram 127.0.0.1.
func healthURL(listenAddr string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("endereço de escuta inválido %q: %w", listenAddr, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/api/health", nil
}

// healthURLs devolve as URLs de /api/health a tentar depois de uma
// atualização: a do endereço configurado e, se for outra, a de
// 127.0.0.1:<porta>, que responde também com a porta pública desligada.
func healthURLs(listenAddr string) ([]string, error) {
	u, err := healthURL(listenAddr)
	if err != nil {
		return nil, err
	}
	local, err := domains.EffectiveListenAddr(listenAddr, false)
	if err != nil {
		return nil, err
	}
	l, err := healthURL(local)
	if err != nil {
		return nil, err
	}
	if l == u {
		return []string{u}, nil
	}
	return []string{u, l}, nil
}

// updateFinish reinicia o serviço após uma atualização e reverte o binário se
// o novo não responder em /api/health. Roda a partir do binário anterior.
func updateFinish(args []string) error {
	fs := flag.NewFlagSet("update-finish", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/korego/config.toml", "caminho do arquivo de configuração")
	exe := fs.String("exe", "", "caminho do binário instalado (obrigatório)")
	_ = fs.Parse(args)
	if *exe == "" {
		return errors.New("--exe é obrigatório")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	urls, err := healthURLs(cfg.ListenAddr)
	if err != nil {
		return err
	}
	httpc := &http.Client{Timeout: 3 * time.Second}

	f := &update.Finisher{
		ExePath: *exe,
		Timeout: 30 * time.Second,
		Restart: func(ctx context.Context) error {
			return exec.CommandContext(ctx, "systemctl", "restart", "korego").Run()
		},
		Health: func(ctx context.Context) error {
			var errs []error
			for _, url := range urls {
				err := checkHealth(ctx, httpc, url)
				if err == nil {
					return nil
				}
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	}
	err = f.Run(context.Background())
	if errors.Is(err, update.ErrRolledBack) {
		log.Printf("%v", err)
		return nil
	}
	return err
}

// checkHealth faz GET em url e exige 200.
func checkHealth(ctx context.Context, httpc *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s retornou %d", url, resp.StatusCode)
	}
	return nil
}
