// Package migrate troca o servidor das portas 80/443: do nginx (ou apache2)
// para o Caddy do KoreGO ("assumir as portas") e de volta ("voltar").
//
// Regra de ouro: nenhum caminho de falha deixa os dois servidores parados.
// Toda falha termina com o servidor anterior ativo e o Caddy em ensaio
// (rolled_back) ou com resultado failed e uma mensagem que diz exatamente o
// que está rodando.
//
// A troca roda com a reconciliação dos domínios pausada (Domains.Pause):
// nem Ensure nem Apply mexem no proxy no meio do caminho, e deploys esperam.
// O motor usa proxy.Switch, que ignora os guardas de Apply.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
)

// TopicMigration é o tópico do hub com o progresso da troca.
const TopicMigration = "proxy-migration"

// Configurações gravadas.
const (
	SettingMigratedFrom  = "proxy.migrated_from"
	SettingMigratedAt    = "proxy.migrated_at" // unix
	SettingLastMigration = "proxy.last_migration"
	// SettingInProgress marca uma troca em andamento (InProgress em JSON);
	// presente na subida, a troca foi interrompida (RecoverInterrupted).
	SettingInProgress = "proxy.migration_in_progress"
)

// Tempos da troca.
const (
	BindRetry      = 5 * time.Second  // nova tentativa do bind em 80/443
	AutoCertWait   = 60 * time.Second // espera pelos certificados automáticos
	bindInterval   = 250 * time.Millisecond
	verifyInterval = 3 * time.Second
	activeWait     = 10 * time.Second // espera a unit ficar ativa
	activeInterval = 500 * time.Millisecond
	trialAttempts  = 3
	trialCacheTTL  = 10 * time.Second
	stepTimeout    = 2 * time.Minute // limite de cada passo de volta
	ensureTimeout  = 30 * time.Second
	existsTTL      = 60 * time.Second // cache de Exists em Info
)

// Resultados.
const (
	OutcomeOK         = "ok"
	OutcomeRolledBack = "rolled_back"
	OutcomeFailed     = "failed"
)

// Operações.
const (
	OpTakeover = "takeover"
	OpRollback = "rollback"
)

// Status de um passo.
const (
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusFailed  = "failed"
)

// Passos publicados em TopicMigration.
const (
	StepTrialCheck     = "trial_check"     // ensaio de todos os domínios
	StepStop           = "stop"            // systemctl stop <unit>
	StepBind           = "bind"            // Caddy em :80/:443
	StepVerify         = "verify"          // verificação em 127.0.0.1:443
	StepDisable        = "disable"         // systemctl disable <unit>
	StepRecord         = "record"          // grava proxy.migrated_from/at
	StepRevertTrial    = "revert_trial"    // Caddy de volta ao ensaio
	StepRevertStart    = "revert_start"    // systemctl start <unit>
	StepRevertActive   = "revert_active"   // confirma a unit ativa
	StepRetake         = "retake"          // último recurso: Caddy reassume 80/443
	StepTrial          = "trial"           // voltar: Caddy em ensaio
	StepEnable         = "enable"          // voltar: systemctl enable
	StepStart          = "start"           // voltar: systemctl start
	StepActive         = "active"          // voltar: confirma a unit ativa
	StepClear          = "clear"           // voltar: apaga proxy.migrated_from/at
	StepRestoreDisable = "restore_disable" // voltar falhou: desativa a unit de novo
)

// Códigos de recusa (HTTP 409).
const (
	CodeNotTrial         = "PROXY_NOT_TRIAL"
	CodeUnsupportedOwner = "UNSUPPORTED_PORT_OWNER"
	CodeRunning          = "MIGRATION_RUNNING"
	CodeNotMigrated      = "PROXY_NOT_MIGRATED"
	CodeDNSNotReady      = "DNS_NOT_READY"
	CodeUpdating         = "UPDATE_IN_PROGRESS"
	CodeDomainsSkipped   = "DOMAINS_SKIPPED"
)

// InterruptedMessage é o resultado gravado quando o KoreGO reinicia no meio
// de uma troca.
const InterruptedMessage = "interrompida (KoreGO reiniciou durante a troca)"

// InProgress é a marca gravada em SettingInProgress.
type InProgress struct {
	Op        string    `json:"op"`
	Unit      string    `json:"unit"`
	StartedAt time.Time `json:"startedAt"`
}

// Refusal é uma recusa antes de mexer em qualquer coisa.
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// Result é o resultado de uma troca.
type Result struct {
	Outcome string `json:"outcome"` // ok | rolled_back | failed
	Message string `json:"message"`
	Code    string `json:"code,omitempty"` // recusa (Refusal.Code)
}

// Event é o progresso publicado em TopicMigration. O último evento tem
// Done=true e Result.
type Event struct {
	Op      string `json:"op"`
	Step    string `json:"step,omitempty"`
	Status  string `json:"status,omitempty"` // running | ok | failed
	Message string `json:"message"`
	Done    bool   `json:"done,omitempty"`
	Result  string `json:"result,omitempty"`
}

// LastMigration é o último resultado gravado (proxy.last_migration).
type LastMigration struct {
	Op      string    `json:"op"`
	Outcome string    `json:"outcome"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// Info é o estado da migração para GET /api/proxy.
type Info struct {
	MigratedFrom string
	MigratedAt   *time.Time
	Last         *LastMigration
	Running      bool
	// RollbackAvailable: há unit registrada e o arquivo dela existe.
	RollbackAvailable bool
}

// Domains é o que o motor usa do serviço de domínios.
type Domains interface {
	Sites(ctx context.Context) ([]proxy.Site, error)
	// SitesReport é Sites mais os domínios gravados que ficaram de fora.
	SitesReport(ctx context.Context) ([]proxy.Site, []proxy.SkippedSite, error)
	CFRanges() []string
	Status() proxy.Status
	// Pause para a reconciliação e os deploys; Resume os solta publicando o
	// status dado.
	Pause()
	Resume(st proxy.Status)
	// Reconcile faz uma volta de reconciliação (fora da pausa).
	Reconcile(ctx context.Context) proxy.Status
	// DNSReady diz se host resolve para esta máquina ou para as faixas da
	// Cloudflare; senão, a mensagem.
	DNSReady(ctx context.Context, host string) (bool, string)
}

// CheckFunc é a assinatura de Check.
type CheckFunc func(ctx context.Context, sites []proxy.Site, addr string, trial bool) []SiteCheck

// Engine executa a troca. Uma troca por vez.
type Engine struct {
	Domains Domains
	Proxy   proxy.Proxy
	Systemd ServiceManager
	DB      *store.DB
	Hub     *events.Hub
	Now     func() time.Time                                 // nil = time.Now
	Sleep   func(ctx context.Context, d time.Duration) error // nil = espera real
	Check   CheckFunc                                        // nil = Check

	mu       sync.Mutex
	running  bool
	updating bool // uma atualização do KoreGO segura a vaga (BlockForUpdate)

	existsMu    sync.Mutex
	existsCache map[string]existsEntry

	cacheMu sync.Mutex
	cacheAt time.Time
	cache   []SiteCheck
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) sleep(ctx context.Context, d time.Duration) error {
	if e.Sleep != nil {
		return e.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (e *Engine) check(ctx context.Context, sites []proxy.Site, addr string, trial bool) []SiteCheck {
	if e.Check != nil {
		return e.Check(ctx, sites, addr, trial)
	}
	return Check(ctx, sites, addr, trial)
}

// Running diz se há troca em andamento.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

type existsEntry struct {
	at time.Time
	ok bool
}

// claim toma a vaga de troca; recusa durante outra troca ou atualização.
func (e *Engine) claim() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.busyLocked(); err != nil {
		return err
	}
	e.running = true
	return nil
}

func (e *Engine) busyLocked() error {
	if e.running {
		return errRunning
	}
	if e.updating {
		return errUpdating
	}
	return nil
}

func (e *Engine) busy() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.busyLocked()
}

// BlockForUpdate impede trocas enquanto o KoreGO se atualiza; recusa
// (MIGRATION_RUNNING) com troca em andamento. release libera a vaga (só
// quando a atualização falha: com sucesso o processo reinicia).
func (e *Engine) BlockForUpdate() (release func(), err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.busyLocked(); err != nil {
		return nil, err
	}
	e.updating = true
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			e.updating = false
			e.mu.Unlock()
		})
	}, nil
}

func (e *Engine) release() {
	e.mu.Lock()
	e.running = false
	e.mu.Unlock()
}

var (
	errRunning  = &Refusal{Code: CodeRunning, Message: "Já há uma troca de servidor em andamento."}
	errUpdating = &Refusal{Code: CodeUpdating, Message: "O KoreGO está sendo atualizado; tente a troca depois que ele reiniciar."}
)

// TrialCheck roda Check em TrialAddr com cache de 10 s. Recusa com
// PROXY_NOT_TRIAL fora do ensaio e MIGRATION_RUNNING durante uma troca.
func (e *Engine) TrialCheck(ctx context.Context) ([]SiteCheck, error) {
	if e.Running() {
		return nil, errRunning
	}
	if st := e.Domains.Status(); st.State != proxy.StateTrial {
		return nil, &Refusal{Code: CodeNotTrial, Message: notTrialMsg(st)}
	}
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	if e.cache != nil && e.now().Sub(e.cacheAt) < trialCacheTTL {
		return e.cache, nil
	}
	sites, err := e.Domains.Sites(ctx)
	if err != nil {
		return nil, err
	}
	res := e.check(ctx, sites, TrialAddr, true)
	if res == nil {
		res = []SiteCheck{}
	}
	e.cache, e.cacheAt = res, e.now()
	return res, nil
}

func notTrialMsg(st proxy.Status) string {
	return fmt.Sprintf("O proxy não está em modo ensaio (estado: %s).", st.State)
}

// Info lê o estado da migração gravado.
func (e *Engine) Info(ctx context.Context) (Info, error) {
	var in Info
	in.Running = e.Running()
	from, ok, err := e.DB.GetSetting(ctx, SettingMigratedFrom)
	if err != nil {
		return in, err
	}
	if ok {
		in.MigratedFrom = from
		if _, valid := supportedUnits[from]; valid && e.Systemd != nil {
			in.RollbackAvailable = e.existsCached(ctx, from)
		}
	}
	if v, ok, err := e.DB.GetSetting(ctx, SettingMigratedAt); err != nil {
		return in, err
	} else if ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			t := time.Unix(n, 0).UTC()
			in.MigratedAt = &t
		}
	}
	if v, ok, err := e.DB.GetSetting(ctx, SettingLastMigration); err != nil {
		return in, err
	} else if ok {
		var lm LastMigration
		if json.Unmarshal([]byte(v), &lm) == nil {
			in.Last = &lm
		}
	}
	return in, nil
}

// existsCached é Systemd.Exists com cache de 60 s (GET /api/proxy é
// consultado com frequência).
func (e *Engine) existsCached(ctx context.Context, unit string) bool {
	e.existsMu.Lock()
	defer e.existsMu.Unlock()
	if c, ok := e.existsCache[unit]; ok && e.now().Sub(c.at) < existsTTL {
		return c.ok
	}
	ok := e.Systemd.Exists(ctx, unit)
	if e.existsCache == nil {
		e.existsCache = map[string]existsEntry{}
	}
	e.existsCache[unit] = existsEntry{at: e.now(), ok: ok}
	return ok
}

// job acompanha uma troca: publica os passos e grava o resultado.
type job struct {
	e  *Engine
	op string
}

func (j *job) step(step, status, msg string) {
	if status == StatusFailed {
		log.Printf("proxy: troca (%s) passo %s falhou: %s", j.op, step, msg)
	}
	if j.e.Hub != nil {
		j.e.Hub.Publish(TopicMigration, Event{Op: j.op, Step: step, Status: status, Message: msg})
	}
}

// record grava o resultado final (e apaga a marca de troca em andamento).
func (j *job) record(ctx context.Context, res Result) {
	log.Printf("proxy: troca (%s) terminou: %s: %s", j.op, res.Outcome, res.Message)
	if err := j.e.DB.DeleteSetting(ctx, SettingInProgress); err != nil {
		log.Printf("proxy: apagar a marca de troca em andamento: %v", err)
	}
	lm := LastMigration{Op: j.op, Outcome: res.Outcome, Message: res.Message, At: j.e.now().UTC()}
	if b, err := json.Marshal(lm); err == nil {
		if err := j.e.DB.SetSetting(ctx, SettingLastMigration, string(b)); err != nil {
			log.Printf("proxy: gravar o resultado da troca: %v", err)
		}
	}
}

// publishDone publica o evento final. Chamado só depois de retomar os
// domínios e liberar a vaga: quem relê GET /api/proxy ao recebê-lo já vê
// migrating=false.
func (j *job) publishDone(res Result) {
	if j.e.Hub != nil {
		j.e.Hub.Publish(TopicMigration, Event{Op: j.op, Message: res.Message, Done: true, Result: res.Outcome})
	}
}

// Takeover roda StartTakeover e espera o resultado. Recusa vira failed com
// Code.
func (e *Engine) Takeover(ctx context.Context) Result {
	ch := make(chan Result, 1)
	if err := e.StartTakeover(ctx, func(r Result) { ch <- r }); err != nil {
		return refusalResult(err)
	}
	return <-ch
}

// Rollback roda StartRollback e espera o resultado.
func (e *Engine) Rollback(ctx context.Context) Result {
	ch := make(chan Result, 1)
	if err := e.StartRollback(ctx, func(r Result) { ch <- r }); err != nil {
		return refusalResult(err)
	}
	return <-ch
}

func refusalResult(err error) Result {
	var ref *Refusal
	if errors.As(err, &ref) {
		return Result{Outcome: OutcomeFailed, Message: ref.Message, Code: ref.Code}
	}
	return Result{Outcome: OutcomeFailed, Message: err.Error()}
}

// session é uma troca com a vaga tomada e os domínios pausados.
type session struct {
	e       *Engine
	st      proxy.Status // estado do proxy lido com a reconciliação parada
	resumed bool
}

// resume solta os domínios publicando st (uma vez).
func (ss *session) resume(st proxy.Status) {
	if !ss.resumed {
		ss.resumed = true
		ss.e.Domains.Resume(st)
	}
}

// end solta os domínios e a vaga.
func (ss *session) end(st proxy.Status) {
	ss.resume(st)
	ss.e.release()
}

// begin toma a vaga de troca, pausa os domínios e lê o estado atual do proxy
// (Ensure com a reconciliação parada). Um pânico depois da pausa a desfaz
// antes de seguir.
func (e *Engine) begin(ctx context.Context) (*session, error) {
	if err := e.claim(); err != nil {
		return nil, err
	}
	ss := &session{e: e}
	paused := false
	defer func() {
		if r := recover(); r != nil {
			if paused {
				ss.resume(proxy.Status{})
			}
			e.release()
			panic(r)
		}
	}()
	e.Domains.Pause()
	paused = true
	ss.st = e.Proxy.Ensure(ctx)
	return ss, nil
}

// guard desfaz a sessão se a parte síncrona entrar em pânico antes de
// entregar a troca ao segundo plano (*started vira true depois de run).
func (ss *session) guard(started *bool) {
	if r := recover(); r != nil {
		if !*started {
			ss.end(ss.st)
		}
		panic(r)
	}
}

// mark grava a marca de troca em andamento.
func (e *Engine) mark(ctx context.Context, op, unit string) error {
	b, err := json.Marshal(InProgress{Op: op, Unit: unit, StartedAt: e.now().UTC()})
	if err != nil {
		return err
	}
	if err := e.DB.SetSetting(ctx, SettingInProgress, string(b)); err != nil {
		return fmt.Errorf("gravar a marca de troca em andamento: %w", err)
	}
	return nil
}

// run executa fn em segundo plano. Ao fim relê o estado do proxy, grava o
// resultado (e apaga a marca), retoma os domínios, libera a vaga, publica o
// evento final e chama onDone. Um pânico em fn vira resultado failed: os domínios são retomados,
// uma reconciliação roda na hora, a unit é religada se ninguém atende 80/443 e o
// estado final vai na mensagem.
func (e *Engine) run(ctx context.Context, ss *session, op, unit string, fn func(context.Context, *job) Result, onDone func(Result)) {
	jctx := context.WithoutCancel(ctx)
	go func() {
		j := &job{e: e, op: op}
		res, panicked := safeRun(jctx, j, fn)
		ectx, cancel := context.WithTimeout(jctx, ensureTimeout)
		final := e.Proxy.Ensure(ectx)
		cancel()
		if panicked {
			ss.resume(final)
			rctx, cancel := context.WithTimeout(jctx, ensureTimeout)
			st := e.Domains.Reconcile(rctx)
			cancel()
			res.Message += e.restartIfBothDown(jctx, unit, st) + e.settle(jctx, unit, st)
		}
		j.record(jctx, res)
		// Solta domínios e vaga antes do done; o defer publica mesmo se a
		// retomada entrar em pânico.
		func() {
			defer j.publishDone(res)
			ss.end(final)
		}()
		if onDone != nil {
			onDone(res)
		}
	}()
}

func safeRun(ctx context.Context, j *job, fn func(context.Context, *job) Result) (res Result, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("proxy: pânico na troca (%s): %v\n%s", j.op, r, debug.Stack())
			res = Result{Outcome: OutcomeFailed, Message: fmt.Sprintf("erro interno na troca: %v.", r)}
			panicked = true
		}
	}()
	return fn(ctx, j), false
}

// settle descreve o estado depois de uma troca interrompida e, com o Caddy
// servindo 80/443 e a unit parada, registra a migração para oferecer Voltar.
func (e *Engine) settle(ctx context.Context, unit string, st proxy.Status) string {
	if _, ok := supportedUnits[unit]; !ok {
		return fmt.Sprintf(" Estado do proxy: %s.", st.State)
	}
	active, err := e.Systemd.Active(ctx, unit)
	switch {
	case err != nil:
		return fmt.Sprintf(" Estado do proxy: %s; não foi possível conferir o %s (%v).", st.State, unit, err)
	case active:
		return fmt.Sprintf(" O %s está ativo; estado do proxy: %s.", unit, st.State)
	case st.State == proxy.StateActive && st.Mode == proxy.ListenNormal:
		if rerr := e.record(ctx, unit); rerr != nil {
			return fmt.Sprintf(" O Caddy está servindo 80/443 e o %s está parado; não foi possível registrar a migração (%v).", unit, rerr)
		}
		return fmt.Sprintf(" O Caddy está servindo 80/443 e o %s está parado; use Voltar se precisar.", unit)
	default:
		return fmt.Sprintf(" ATENÇÃO: o %s está parado e o proxy está em %s. Rode \"systemctl start %s\" no servidor se os sites estiverem fora do ar.", unit, st.State, unit)
	}
}

// RecoverInterrupted roda na subida: com a marca de troca em andamento
// presente (o KoreGO caiu ou reiniciou no meio), reconcilia o proxy (o modo
// gravado decide: ensaio segue ensaio), religa a unit se ninguém atende
// 80/443 (restartIfBothDown), grava o resultado failed com
// InterruptedMessage, apaga a marca e, com o Caddy servindo e a unit parada,
// registra a migração para oferecer Voltar.
func (e *Engine) RecoverInterrupted(ctx context.Context) error {
	v, ok, err := e.DB.GetSetting(ctx, SettingInProgress)
	if err != nil || !ok {
		return err
	}
	var m InProgress
	if json.Unmarshal([]byte(v), &m) != nil || (m.Op != OpTakeover && m.Op != OpRollback) {
		m.Op = OpTakeover
	}
	if err := e.claim(); err != nil {
		return err
	}
	j := &job{e: e, op: m.Op}
	var res Result
	// Defers em ordem inversa: libera a vaga e só então publica o done.
	defer func() {
		if res.Outcome != "" {
			j.publishDone(res)
		}
	}()
	defer e.release()
	st := e.Domains.Reconcile(ctx)
	note := e.restartIfBothDown(ctx, m.Unit, st)
	res = Result{Outcome: OutcomeFailed, Message: InterruptedMessage + "." + note + e.settle(ctx, m.Unit, st)}
	j.record(ctx, res)
	return nil
}

// restartIfBothDown roda na subida depois de uma troca interrompida: o Caddy
// ficou em ensaio (o modo gravado não sai sozinho) com as portas livres e a
// unit parada, então nenhum servidor atende 80/443. Religa a unit; se ela não
// subir, o Caddy reassume 80/443 (último recurso, como em revert). Devolve
// uma nota para a mensagem ("" se não fez nada).
func (e *Engine) restartIfBothDown(ctx context.Context, unit string, st proxy.Status) string {
	if _, ok := supportedUnits[unit]; !ok || e.Systemd == nil {
		return ""
	}
	if st.State != proxy.StateTrial || st.Process != "" || !e.Systemd.Exists(ctx, unit) {
		return ""
	}
	if active, err := e.Systemd.Active(ctx, unit); err != nil || active {
		return ""
	}
	serr := e.startUnit(ctx, unit)
	if serr == nil {
		return " O " + unit + " foi religado."
	}
	sites, err := e.Domains.Sites(ctx)
	if err == nil {
		err = e.bindNormal(ctx, sites, e.Domains.CFRanges())
	}
	if err != nil {
		return fmt.Sprintf(" ATENÇÃO: o %s não subiu (%v) e o Caddy não conseguiu assumir 80/443 (%v).", unit, serr, err)
	}
	return fmt.Sprintf(" O %s não subiu (%v); o Caddy assumiu 80/443.", unit, serr)
}

// PrecheckTakeover são as recusas de assumir que não mexem em nada nem
// pausam a reconciliação (troca ou atualização em andamento, estado trial,
// dono suportado, DNS dos domínios auto), para a API responder 409 antes de
// consumir o código de confirmação. StartTakeover confere de novo o estado e
// o dono sob a pausa.
func (e *Engine) PrecheckTakeover(ctx context.Context) error {
	if err := e.busy(); err != nil {
		return err
	}
	st := e.Domains.Status()
	if st.State != proxy.StateTrial {
		return &Refusal{Code: CodeNotTrial, Message: notTrialMsg(st)}
	}
	// Process vazio: portas livres (ninguém para parar).
	if _, ok := e.Systemd.UnitForProcess(st.Process); st.Process != "" && !ok {
		return unsupportedOwner(st)
	}
	sites, skipped, err := e.Domains.SitesReport(ctx)
	if err != nil {
		return fmt.Errorf("montar os sites: %w", err)
	}
	if len(skipped) > 0 {
		return domainsSkipped(skipped)
	}
	if bad := e.dnsProblems(ctx, sites); len(bad) > 0 {
		return &Refusal{Code: CodeDNSNotReady, Message: "O DNS destes domínios com certificado automático não aponta para este servidor: " +
			strings.Join(bad, ", ") + ". Sem isso o certificado não sai e a troca volta sozinha; corrija o DNS antes de assumir as portas."}
	}
	return nil
}

// PrecheckRollback são as recusas baratas de voltar.
func (e *Engine) PrecheckRollback(ctx context.Context) error {
	if err := e.busy(); err != nil {
		return err
	}
	_, err := e.rollbackUnit(ctx)
	return err
}

// domainsSkipped recusa assumir com domínios gravados fora da config do
// proxy: servidos hoje pelo servidor anterior, sumiriam do ar.
func domainsSkipped(skipped []proxy.SkippedSite) *Refusal {
	parts := make([]string, 0, len(skipped))
	for _, s := range skipped {
		if s.Reason == "" {
			parts = append(parts, s.Host)
			continue
		}
		parts = append(parts, s.Host+" ("+s.Reason+")")
	}
	return &Refusal{Code: CodeDomainsSkipped, Message: "Estes domínios cadastrados ficariam fora do Caddy e sairiam do ar: " +
		strings.Join(parts, "; ") + ". Corrija ou remova esses domínios antes de assumir as portas."}
}

func unsupportedOwner(st proxy.Status) *Refusal {
	return &Refusal{Code: CodeUnsupportedOwner, Message: fmt.Sprintf(
		"As portas 80/443 estão com %s. Só nginx e apache2 gerenciados pelo systemd são suportados: pare esse processo manualmente.",
		st.Process)}
}

// StartTakeover confere as pré-condições (estado trial; dono das portas é
// nginx ou apache2 do systemd, com a unit existente e ativa) e, passando, assume as portas em segundo
// plano; onDone recebe o resultado. Recusa com *Refusal sem mexer em nada.
func (e *Engine) StartTakeover(ctx context.Context, onDone func(Result)) error {
	ss, err := e.begin(ctx)
	if err != nil {
		return err
	}
	started := false
	defer ss.guard(&started)
	unit, stop, err := e.takeoverUnit(ctx, ss.st)
	if err != nil {
		ss.end(ss.st)
		return err
	}
	sites, skipped, err := e.Domains.SitesReport(ctx)
	if err != nil {
		ss.end(ss.st)
		return fmt.Errorf("montar os sites: %w", err)
	}
	if len(skipped) > 0 {
		ss.end(ss.st)
		return domainsSkipped(skipped)
	}
	if err := e.mark(ctx, OpTakeover, unit); err != nil {
		ss.end(ss.st)
		return err
	}
	e.run(ctx, ss, OpTakeover, unit, func(ctx context.Context, j *job) Result {
		return e.takeover(ctx, j, unit, stop, sites)
	}, onDone)
	started = true
	return nil
}

// dnsProblems lista os hosts auto cujo DNS não aponta para cá.
func (e *Engine) dnsProblems(ctx context.Context, sites []proxy.Site) []string {
	var (
		mu  sync.Mutex
		bad []string
		wg  sync.WaitGroup
	)
	sem := make(chan struct{}, checkConcurrency)
	for _, s := range sites {
		if !isAuto(s) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ok, _ := e.Domains.DNSReady(ctx, s.Host); !ok {
				mu.Lock()
				bad = append(bad, s.Host)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	slices.Sort(bad)
	return bad
}

// takeoverUnit decide a unit da troca e se ela precisa ser parada.
//
// Com dono nas portas: a unit dele (nginx/apache2 do systemd, existente e
// ativa), parada na troca. Com as portas livres (Process vazio): a unit
// lembrada do ensaio (proxy.trial_unit), se suportada e existente — parada só
// se estiver ativa (subindo de novo) — ou "" (sem unit: sem Voltar).
func (e *Engine) takeoverUnit(ctx context.Context, st proxy.Status) (unit string, stop bool, err error) {
	if st.State != proxy.StateTrial {
		return "", false, &Refusal{Code: CodeNotTrial, Message: notTrialMsg(st)}
	}
	if st.Process == "" {
		unit := e.trialUnit(ctx)
		if unit == "" {
			return "", false, nil
		}
		active, aerr := e.Systemd.Active(ctx, unit)
		return unit, aerr != nil || active, nil
	}
	unit, ok := e.Systemd.UnitForProcess(st.Process)
	if !ok {
		return "", false, unsupportedOwner(st)
	}
	if !e.Systemd.Exists(ctx, unit) {
		return "", false, &Refusal{Code: CodeUnsupportedOwner, Message: fmt.Sprintf(
			"As portas 80/443 estão com %s, mas a unit %s.service não existe: pare esse processo manualmente.", st.Process, unit)}
	}
	active, err := e.Systemd.Active(ctx, unit)
	if err != nil || !active {
		return "", false, &Refusal{Code: CodeUnsupportedOwner, Message: fmt.Sprintf(
			"As portas 80/443 estão com %s, mas a unit %s.service não está ativa (o processo não roda pelo systemd): pare esse processo manualmente.",
			st.Process, unit)}
	}
	return unit, true, nil
}

// trialUnit lê a unit lembrada do ensaio (proxy.trial_unit); "" se não houver,
// não for suportada ou o arquivo dela não existir.
func (e *Engine) trialUnit(ctx context.Context) string {
	unit, ok, err := e.DB.GetSetting(ctx, proxy.SettingTrialUnit)
	if err != nil || !ok {
		return ""
	}
	if _, valid := supportedUnits[unit]; !valid || !e.Systemd.Exists(ctx, unit) {
		return ""
	}
	return unit
}

// takeover são os passos 2–5 do §4.2, com o Caddy em ensaio. unit "" (portas
// livres, sem unit lembrada): sem stop, disable nem registro. stop=false: a
// unit já está parada.
func (e *Engine) takeover(ctx context.Context, j *job, unit string, stop bool, sites []proxy.Site) Result {
	cf := e.Domains.CFRanges()

	j.step(StepTrialCheck, StatusRunning, "ensaio de todos os domínios em "+TrialAddr)
	if bad := failures(e.check(ctx, sites, TrialAddr, true)); len(bad) > 0 {
		msg := "o ensaio falhou em " + strings.Join(bad, "; ") + "; nada foi alterado"
		j.step(StepTrialCheck, StatusFailed, msg)
		return Result{Outcome: OutcomeFailed, Message: msg}
	}
	j.step(StepTrialCheck, StatusOK, fmt.Sprintf("%d domínio(s) ok no ensaio", len(sites)))

	if stop {
		j.step(StepStop, StatusRunning, "parando o "+unit)
		if err := e.Systemd.Stop(ctx, unit); err != nil {
			j.step(StepStop, StatusFailed, err.Error())
			return e.revert(ctx, j, unit, sites, cf, "parar o "+unit+": "+err.Error())
		}
		j.step(StepStop, StatusOK, unit+" parado")
	} else {
		j.step(StepStop, StatusOK, "portas 80/443 já livres; nada a parar")
	}

	j.step(StepBind, StatusRunning, "Caddy assumindo as portas 80/443")
	if err := e.bindNormal(ctx, sites, cf); err != nil {
		j.step(StepBind, StatusFailed, err.Error())
		return e.revert(ctx, j, unit, sites, cf, "o Caddy não conseguiu escutar em 80/443: "+err.Error())
	}
	j.step(StepBind, StatusOK, "Caddy escutando em 80/443")

	j.step(StepVerify, StatusRunning, "verificando todos os domínios em "+NormalAddr)
	if err := e.verify(ctx, j, sites); err != nil {
		j.step(StepVerify, StatusFailed, err.Error())
		return e.revert(ctx, j, unit, sites, cf, "a verificação falhou: "+err.Error())
	}
	j.step(StepVerify, StatusOK, fmt.Sprintf("%d domínio(s) ok", len(sites)))

	if unit == "" {
		return Result{Outcome: OutcomeOK, Message: "O Caddy assumiu as portas 80/443 (estavam livres; não há servidor anterior para Voltar)."}
	}

	var warns []string
	j.step(StepDisable, StatusRunning, "desativando o "+unit+" no boot")
	if err := e.Systemd.Disable(ctx, unit); err != nil {
		j.step(StepDisable, StatusFailed, err.Error())
		warns = append(warns, "não foi possível desativar o "+unit+" no boot ("+err.Error()+"); se ele subir no boot, o Caddy volta ao ensaio")
	} else {
		j.step(StepDisable, StatusOK, unit+" desativado no boot")
	}

	j.step(StepRecord, StatusRunning, "registrando a migração")
	if err := e.record(ctx, unit); err != nil {
		j.step(StepRecord, StatusFailed, err.Error())
		warns = append(warns, "não foi possível registrar a migração ("+err.Error()+"); o botão Voltar não vai aparecer")
	} else {
		j.step(StepRecord, StatusOK, "migração registrada")
	}

	msg := "O Caddy assumiu as portas 80/443; o " + unit + " foi parado."
	if !stop {
		msg = "O Caddy assumiu as portas 80/443 (estavam livres); o " + unit + " fica desativado no boot."
	}
	if len(warns) > 0 {
		msg += " Aviso: " + strings.Join(warns, "; ") + "."
	}
	return Result{Outcome: OutcomeOK, Message: msg}
}

func (e *Engine) record(ctx context.Context, unit string) error {
	if err := e.DB.SetSetting(ctx, SettingMigratedFrom, unit); err != nil {
		return err
	}
	return e.DB.SetSetting(ctx, SettingMigratedAt, strconv.FormatInt(e.now().Unix(), 10))
}

// failures lista "host: mensagem" dos sites que não passaram.
func failures(checks []SiteCheck) []string {
	var out []string
	for _, c := range checks {
		if !c.OK {
			out = append(out, c.Host+": "+c.Message)
		}
	}
	return out
}

// bindNormal carrega o modo normal, tentando de novo por até BindRetry
// enquanto o processo anterior solta as portas. Carga recusada não muda a
// config do Caddy.
func (e *Engine) bindNormal(ctx context.Context, sites []proxy.Site, cf []string) error {
	deadline := e.now().Add(BindRetry)
	for {
		err := e.Proxy.Switch(ctx, sites, proxy.ListenNormal, cf)
		if err == nil {
			return nil
		}
		if !e.now().Before(deadline) {
			return err
		}
		if serr := e.sleep(ctx, bindInterval); serr != nil {
			return err
		}
	}
}

// verify confere todos os sites em NormalAddr. Sites com certificado próprio
// passam na hora; sites auto têm até AutoCertWait para emitir.
func (e *Engine) verify(ctx context.Context, j *job, sites []proxy.Site) error {
	start := e.now()
	pending := sites
	for {
		byHost := make(map[string]proxy.Site, len(pending))
		for _, s := range pending {
			byHost[s.Host] = s
		}
		var retry []proxy.Site
		var waiting []string
		for _, c := range e.check(ctx, pending, NormalAddr, false) {
			if c.OK {
				continue
			}
			s, known := byHost[c.Host]
			if !known || !isAuto(s) {
				return fmt.Errorf("%s: %s", c.Host, c.Message)
			}
			retry = append(retry, s)
			waiting = append(waiting, c.Host+": "+c.Message)
		}
		if len(retry) == 0 {
			return nil
		}
		if e.now().Sub(start) >= AutoCertWait {
			return fmt.Errorf("certificado automático não saiu em %s: %s", AutoCertWait, strings.Join(waiting, "; "))
		}
		j.step(StepVerify, StatusRunning, fmt.Sprintf("aguardando o certificado de %d domínio(s)", len(retry)))
		if err := e.sleep(ctx, verifyInterval); err != nil {
			return fmt.Errorf("verificação interrompida: %w", err)
		}
		pending = retry
	}
}

// revert desfaz a troca (passo 4 do §4.2): Caddy em ensaio, start da unit e
// confirmação. Se a unit não voltar, o Caddy reassume 80/443 para nunca
// deixar os dois parados.
func (e *Engine) revert(ctx context.Context, j *job, unit string, sites []proxy.Site, cf []string, reason string) Result {
	// A volta roda até o fim mesmo com ctx cancelado.
	ctx = context.WithoutCancel(ctx)

	j.step(StepRevertTrial, StatusRunning, "Caddy de volta ao modo ensaio")
	terr := e.switchTrial(ctx, sites, cf)
	if terr != nil {
		j.step(StepRevertTrial, StatusFailed, terr.Error())
	} else {
		j.step(StepRevertTrial, StatusOK, "Caddy em modo ensaio")
	}

	if unit == "" {
		// Portas livres e nenhuma unit conhecida: não há servidor para religar.
		if terr != nil {
			return Result{Outcome: OutcomeFailed, Message: fmt.Sprintf(
				"A troca falhou (%s) e o Caddy não voltou ao modo ensaio (%v); confira os domínios.", reason, terr)}
		}
		return Result{Outcome: OutcomeRolledBack, Message: fmt.Sprintf(
			"voltou para o modo ensaio: %s. As portas 80/443 seguem livres (sem servidor anterior para religar).", reason)}
	}

	j.step(StepRevertStart, StatusRunning, "iniciando o "+unit)
	serr := e.startUnit(ctx, unit)
	if serr != nil {
		j.step(StepRevertStart, StatusFailed, serr.Error())
	} else {
		j.step(StepRevertStart, StatusOK, unit+" ativo")
	}

	if serr == nil {
		if terr != nil {
			// O unit só sobe com as portas livres: o Caddy não está nelas.
			return Result{Outcome: OutcomeFailed, Message: fmt.Sprintf(
				"voltou para o %s: %s. O %s está ativo em 80/443, mas o Caddy não voltou ao modo ensaio (%v).",
				unit, reason, unit, terr)}
		}
		return Result{Outcome: OutcomeRolledBack, Message: fmt.Sprintf("voltou para o %s: %s", unit, reason)}
	}

	// O unit não voltou: os dois estariam parados. O Caddy reassume.
	j.step(StepRetake, StatusRunning, "o "+unit+" não voltou; Caddy reassumindo 80/443")
	if rerr := e.bindNormal(ctx, sites, cf); rerr != nil {
		j.step(StepRetake, StatusFailed, rerr.Error())
		return Result{Outcome: OutcomeFailed, Message: fmt.Sprintf(
			"ATENÇÃO: nenhum servidor está servindo 80/443. A troca falhou (%s), o %s não voltou (%v) e o Caddy não conseguiu reassumir (%v). Rode `systemctl start %s` no servidor.",
			reason, unit, serr, rerr, unit)}
	}
	j.step(StepRetake, StatusOK, "Caddy servindo 80/443")
	note := ""
	if err := e.record(ctx, unit); err != nil {
		note = " Não foi possível registrar a migração (" + err.Error() + ")."
	}
	return Result{Outcome: OutcomeFailed, Message: fmt.Sprintf(
		"A troca falhou (%s) e o %s não voltou (%v). O Caddy voltou a servir 80/443 para os sites não ficarem fora do ar; confira os domínios que falharam e use Voltar quando o %s estiver em ordem.%s",
		reason, unit, serr, unit, note)}
}

// switchTrial põe o Caddy em ensaio, com algumas tentativas.
func (e *Engine) switchTrial(ctx context.Context, sites []proxy.Site, cf []string) error {
	var err error
	for i := 0; i < trialAttempts; i++ {
		if err = e.Proxy.Switch(ctx, sites, proxy.ListenTrial, cf); err == nil {
			return nil
		}
		if i < trialAttempts-1 {
			_ = e.sleep(ctx, bindInterval)
		}
	}
	return err
}

// startUnit roda systemctl start e espera a unit ficar ativa.
func (e *Engine) startUnit(ctx context.Context, unit string) error {
	sctx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()
	if err := e.Systemd.Start(sctx, unit); err != nil {
		return err
	}
	return e.waitActive(ctx, unit)
}

// waitActive espera a unit ficar ativa por até activeWait.
func (e *Engine) waitActive(ctx context.Context, unit string) error {
	deadline := e.now().Add(activeWait)
	for {
		active, err := e.Systemd.Active(ctx, unit)
		if err == nil && active {
			return nil
		}
		if !e.now().Before(deadline) {
			if err != nil {
				return fmt.Errorf("conferir se o %s está ativo: %w", unit, err)
			}
			return fmt.Errorf("o %s não ficou ativo", unit)
		}
		if serr := e.sleep(ctx, activeInterval); serr != nil {
			return fmt.Errorf("o %s não ficou ativo: %w", unit, serr)
		}
	}
}

// StartRollback volta para o servidor anterior (§4.3) em segundo plano. Só
// com proxy.migrated_from gravado (nginx ou apache2) e o arquivo da unit
// existente; senão *Refusal PROXY_NOT_MIGRATED.
func (e *Engine) StartRollback(ctx context.Context, onDone func(Result)) error {
	ss, err := e.begin(ctx)
	if err != nil {
		return err
	}
	started := false
	defer ss.guard(&started)
	unit, err := e.rollbackUnit(ctx)
	if err != nil {
		ss.end(ss.st)
		return err
	}
	if err := e.mark(ctx, OpRollback, unit); err != nil {
		ss.end(ss.st)
		return err
	}
	e.run(ctx, ss, OpRollback, unit, func(ctx context.Context, j *job) Result {
		return e.rollback(ctx, j, unit)
	}, onDone)
	started = true
	return nil
}

func (e *Engine) rollbackUnit(ctx context.Context) (string, error) {
	unit, ok, err := e.DB.GetSetting(ctx, SettingMigratedFrom)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", &Refusal{Code: CodeNotMigrated, Message: "Não há servidor anterior registrado para voltar."}
	}
	if _, valid := supportedUnits[unit]; !valid {
		return "", &Refusal{Code: CodeNotMigrated, Message: fmt.Sprintf("O servidor anterior registrado (%q) não é suportado.", unit)}
	}
	if !e.Systemd.Exists(ctx, unit) {
		return "", &Refusal{Code: CodeNotMigrated, Message: fmt.Sprintf("A unit %s.service não existe mais: não dá para voltar.", unit)}
	}
	return unit, nil
}

func (e *Engine) rollback(ctx context.Context, j *job, unit string) Result {
	ctx = context.WithoutCancel(ctx)
	sites, err := e.Domains.Sites(ctx)
	if err != nil {
		return Result{Outcome: OutcomeFailed, Message: "montar os sites: " + err.Error() + "; nada mudou"}
	}
	cf := e.Domains.CFRanges()

	j.step(StepTrial, StatusRunning, "Caddy saindo de 80/443 (modo ensaio)")
	if err := e.Proxy.Switch(ctx, sites, proxy.ListenTrial, cf); err != nil {
		if !errors.Is(err, proxy.ErrInactive) {
			j.step(StepTrial, StatusFailed, err.Error())
			return Result{Outcome: OutcomeFailed, Message: "não foi possível tirar o Caddy de 80/443 (" + err.Error() + "); nada mudou, o Caddy continua servindo"}
		}
		// Caddy fora do ar: as portas já estão livres para a unit.
		j.step(StepTrial, StatusOK, "Caddy fora do ar; portas livres")
	} else {
		j.step(StepTrial, StatusOK, "Caddy em modo ensaio")
	}

	var warns []string
	j.step(StepEnable, StatusRunning, "ativando o "+unit+" no boot")
	if err := e.Systemd.Enable(ctx, unit); err != nil {
		j.step(StepEnable, StatusFailed, err.Error())
		warns = append(warns, "não foi possível ativar o "+unit+" no boot ("+err.Error()+")")
	} else {
		j.step(StepEnable, StatusOK, unit+" ativado no boot")
	}

	j.step(StepStart, StatusRunning, "iniciando o "+unit)
	serr := e.startUnit(ctx, unit)
	if serr == nil {
		j.step(StepStart, StatusOK, unit+" ativo")
		j.step(StepClear, StatusRunning, "apagando o registro da migração")
		if err := e.clear(ctx); err != nil {
			j.step(StepClear, StatusFailed, err.Error())
			warns = append(warns, "não foi possível apagar o registro da migração ("+err.Error()+")")
		} else {
			j.step(StepClear, StatusOK, "registro apagado")
		}
		msg := "Voltou para o " + unit + "; o Caddy está em modo ensaio."
		if len(warns) > 0 {
			msg += " Aviso: " + strings.Join(warns, "; ") + "."
		}
		return Result{Outcome: OutcomeOK, Message: msg}
	}
	j.step(StepStart, StatusFailed, serr.Error())

	// A unit não subiu: o Caddy reassume para não deixar os dois parados.
	j.step(StepRetake, StatusRunning, "o "+unit+" não subiu; Caddy reassumindo 80/443")
	if rerr := e.bindNormal(ctx, sites, cf); rerr != nil {
		j.step(StepRetake, StatusFailed, rerr.Error())
		return Result{Outcome: OutcomeFailed, Message: fmt.Sprintf(
			"ATENÇÃO: nenhum servidor está servindo 80/443. O %s não subiu (%v) e o Caddy não conseguiu reassumir (%v). Rode `systemctl start %s` no servidor.",
			unit, serr, rerr, unit)}
	}
	j.step(StepRetake, StatusOK, "Caddy servindo 80/443")
	j.step(StepRestoreDisable, StatusRunning, "desativando o "+unit+" no boot de novo")
	if err := e.Systemd.Disable(ctx, unit); err != nil {
		j.step(StepRestoreDisable, StatusFailed, err.Error())
	} else {
		j.step(StepRestoreDisable, StatusOK, unit+" desativado no boot")
	}
	return Result{Outcome: OutcomeFailed, Message: fmt.Sprintf(
		"O %s não subiu (%v). O Caddy voltou a servir 80/443; nada mais mudou.", unit, serr)}
}

func (e *Engine) clear(ctx context.Context) error {
	if err := e.DB.DeleteSetting(ctx, SettingMigratedFrom); err != nil {
		return err
	}
	return e.DB.DeleteSetting(ctx, SettingMigratedAt)
}
