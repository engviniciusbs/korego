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
)

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
	CFRanges() []string
	Status() proxy.Status
	// Pause para a reconciliação e os deploys; Resume os solta publicando o
	// status dado.
	Pause()
	Resume(st proxy.Status)
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

	mu      sync.Mutex
	running bool

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

func (e *Engine) claim() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return false
	}
	e.running = true
	return true
}

func (e *Engine) release() {
	e.mu.Lock()
	e.running = false
	e.mu.Unlock()
}

var errRunning = &Refusal{Code: CodeRunning, Message: "Já há uma troca de servidor em andamento."}

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
			in.RollbackAvailable = e.Systemd.Exists(ctx, from)
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

// finish grava e publica o resultado final.
func (j *job) finish(ctx context.Context, res Result) {
	log.Printf("proxy: troca (%s) terminou: %s: %s", j.op, res.Outcome, res.Message)
	lm := LastMigration{Op: j.op, Outcome: res.Outcome, Message: res.Message, At: j.e.now().UTC()}
	if b, err := json.Marshal(lm); err == nil {
		if err := j.e.DB.SetSetting(ctx, SettingLastMigration, string(b)); err != nil {
			log.Printf("proxy: gravar o resultado da troca: %v", err)
		}
	}
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

// begin toma a vaga de troca, pausa os domínios e lê o estado atual do proxy
// (Ensure com a reconciliação parada). Devolve a função que desfaz isso.
func (e *Engine) begin(ctx context.Context) (proxy.Status, func(proxy.Status), error) {
	if !e.claim() {
		return proxy.Status{}, nil, errRunning
	}
	e.Domains.Pause()
	st := e.Proxy.Ensure(ctx)
	end := func(final proxy.Status) {
		e.Domains.Resume(final)
		e.release()
	}
	return st, end, nil
}

// run executa fn em segundo plano com a pausa já tomada e, ao fim, relê o
// estado do proxy, retoma os domínios, grava o resultado e chama onDone.
func (e *Engine) run(ctx context.Context, op string, end func(proxy.Status), fn func(context.Context, *job) Result, onDone func(Result)) {
	jctx := context.WithoutCancel(ctx)
	go func() {
		j := &job{e: e, op: op}
		res := fn(jctx, j)
		ectx, cancel := context.WithTimeout(jctx, ensureTimeout)
		final := e.Proxy.Ensure(ectx)
		cancel()
		j.finish(jctx, res)
		end(final)
		if onDone != nil {
			onDone(res)
		}
	}()
}

// StartTakeover confere as pré-condições (estado trial; dono das portas é
// nginx ou apache2 do systemd, com a unit existente e ativa) e, passando,
// assume as portas em segundo plano; onDone recebe o resultado. Recusa com
// *Refusal sem mexer em nada.
func (e *Engine) StartTakeover(ctx context.Context, onDone func(Result)) error {
	st, end, err := e.begin(ctx)
	if err != nil {
		return err
	}
	unit, err := e.takeoverUnit(ctx, st)
	if err != nil {
		end(st)
		return err
	}
	e.run(ctx, OpTakeover, end, func(ctx context.Context, j *job) Result {
		return e.takeover(ctx, j, unit)
	}, onDone)
	return nil
}

func (e *Engine) takeoverUnit(ctx context.Context, st proxy.Status) (string, error) {
	if st.State != proxy.StateTrial {
		return "", &Refusal{Code: CodeNotTrial, Message: notTrialMsg(st)}
	}
	unit, ok := e.Systemd.UnitForProcess(st.Process)
	if !ok {
		return "", &Refusal{Code: CodeUnsupportedOwner, Message: fmt.Sprintf(
			"As portas 80/443 estão com %s. Só nginx e apache2 gerenciados pelo systemd são suportados: pare esse processo manualmente.",
			st.Process)}
	}
	if !e.Systemd.Exists(ctx, unit) {
		return "", &Refusal{Code: CodeUnsupportedOwner, Message: fmt.Sprintf(
			"As portas 80/443 estão com %s, mas a unit %s.service não existe: pare esse processo manualmente.", st.Process, unit)}
	}
	active, err := e.Systemd.Active(ctx, unit)
	if err != nil || !active {
		return "", &Refusal{Code: CodeUnsupportedOwner, Message: fmt.Sprintf(
			"As portas 80/443 estão com %s, mas a unit %s.service não está ativa (o processo não roda pelo systemd): pare esse processo manualmente.",
			st.Process, unit)}
	}
	return unit, nil
}

// takeover são os passos 2–5 do §4.2, com o Caddy em ensaio e o unit ativo.
func (e *Engine) takeover(ctx context.Context, j *job, unit string) Result {
	sites, err := e.Domains.Sites(ctx)
	if err != nil {
		return Result{Outcome: OutcomeFailed, Message: "montar os sites: " + err.Error() + "; nada foi alterado"}
	}
	cf := e.Domains.CFRanges()

	j.step(StepTrialCheck, StatusRunning, "ensaio de todos os domínios em "+TrialAddr)
	if bad := failures(e.check(ctx, sites, TrialAddr, true)); len(bad) > 0 {
		msg := "o ensaio falhou em " + strings.Join(bad, "; ") + "; nada foi alterado"
		j.step(StepTrialCheck, StatusFailed, msg)
		return Result{Outcome: OutcomeFailed, Message: msg}
	}
	j.step(StepTrialCheck, StatusOK, fmt.Sprintf("%d domínio(s) ok no ensaio", len(sites)))

	j.step(StepStop, StatusRunning, "parando o "+unit)
	if err := e.Systemd.Stop(ctx, unit); err != nil {
		j.step(StepStop, StatusFailed, err.Error())
		return e.revert(ctx, j, unit, sites, cf, "parar o "+unit+": "+err.Error())
	}
	j.step(StepStop, StatusOK, unit+" parado")

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
	st, end, err := e.begin(ctx)
	if err != nil {
		return err
	}
	unit, err := e.rollbackUnit(ctx)
	if err != nil {
		end(st)
		return err
	}
	e.run(ctx, OpRollback, end, func(ctx context.Context, j *job) Result {
		return e.rollback(ctx, j, unit)
	}, onDone)
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
