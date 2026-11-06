package monitor

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/docker"
)

// logFlushEvery é o intervalo máximo entre a chegada de uma linha e a sua
// publicação.
const logFlushEvery = 100 * time.Millisecond

var containerIDRe = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// Mensagens publicadas nos tópicos de containers.
type (
	connState struct {
		Action string `json:"action"`
	} // {"action":"unavailable"|"reconnected"}
	sourceError struct {
		Error string `json:"error"`
	} // {"error":"INVALID_CONTAINER_ID"}
	logLines struct {
		Lines []docker.LogLine `json:"lines"`
	} // {"lines":[...]}
	streamEnded struct {
		Ended bool `json:"ended"`
	} // {"ended":true}
	statsStopped struct {
		Running bool `json:"running"`
	} // {"running":false}
)

// topicContainerID devolve o ID depois do primeiro ':' do tópico, se válido.
func topicContainerID(topic string) (string, bool) {
	_, id, ok := strings.Cut(topic, ":")
	return id, ok && containerIDRe.MatchString(id)
}

// sleepCtx espera d; devolve false se ctx foi cancelado antes.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ContainerEventsSource publica no tópico "containers" cada docker.Event.
// Quando Events falha ou termina, publica {"action":"unavailable"} e, a cada
// retry, testa o Docker com Ping; ao voltar, publica {"action":"reconnected"}
// e assina os eventos de novo.
func ContainerEventsSource(c docker.Client, retry time.Duration) events.SourceFunc {
	return func(ctx context.Context, _ string, publish func(any)) {
		down := false
		for {
			if down {
				if c.Ping(ctx) != nil {
					if !sleepCtx(ctx, retry) {
						return
					}
					continue
				}
				if ctx.Err() != nil {
					return
				}
				publish(connState{Action: "reconnected"})
				down = false
			}
			_ = c.Events(ctx, func(e docker.Event) { publish(e) })
			if ctx.Err() != nil {
				return
			}
			publish(connState{Action: "unavailable"})
			down = true
			if !sleepCtx(ctx, retry) {
				return
			}
		}
	}
}

// logsFirstTail é quantas linhas o primeiro stream da fonte de logs traz:
// cobre o intervalo entre o tail da REST e o início da fonte sem Since (o
// Since faz o dockerd varrer o arquivo de log inteiro).
const logsFirstTail = 200

// ContainerLogsSource segue os logs do container do tópico
// "container-logs:<id>": o primeiro stream começa com as últimas
// logsFirstTail linhas, e as linhas são publicadas como {"lines":[...]} a
// cada 100 ms ou 200 linhas. Quando o stream termina, publica {"ended":true}
// (uma vez até chegarem linhas novas); a cada retry inspeciona o container e
// só volta a seguir, a partir da última linha publicada, quando ele está
// rodando. ID inválido ou container inexistente publicam {"error":...} e
// encerram a fonte.
func ContainerLogsSource(c docker.Client, retry time.Duration) events.SourceFunc {
	return func(ctx context.Context, topic string, publish func(any)) {
		id, ok := topicContainerID(topic)
		if !ok {
			publish(sourceError{Error: "INVALID_CONTAINER_ID"})
			return
		}
		b := newLogBatcher("")
		ended := false
		for first := true; ; first = false {
			if !first {
				// Parado, o Logs com follow volta logo depois de varrer o
				// arquivo: só segue de novo com o container rodando.
				d, err := c.Inspect(ctx, id)
				if ctx.Err() != nil {
					return
				}
				if errors.Is(err, docker.ErrNotFound) {
					publish(sourceError{Error: "CONTAINER_NOT_FOUND"})
					return
				}
				if err != nil || d.State != "running" {
					if !sleepCtx(ctx, retry) {
						return
					}
					continue
				}
			}
			got, err := followLogs(ctx, c, id, b, publish)
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, docker.ErrNotFound) {
				publish(sourceError{Error: "CONTAINER_NOT_FOUND"})
				return
			}
			if got {
				ended = false
			}
			if !ended {
				publish(streamEnded{Ended: true})
				ended = true
			}
			if !sleepCtx(ctx, retry) {
				return
			}
		}
	}
}

// followLogs lê um stream de logs até ele terminar, publicando os lotes.
// Sem linha publicada ainda, pede as últimas logsFirstTail; depois, retoma a
// partir da última. Devolve se alguma linha nova foi aceita e o erro de Logs.
// As linhas pendentes são publicadas antes de voltar (exceto com ctx
// cancelado).
func followLogs(ctx context.Context, c docker.Client, id string, b *logBatcher, publish func(any)) (bool, error) {
	o := docker.LogOptions{Follow: true}
	if o.Since = b.resume(); o.Since == "" {
		o.Tail = logsFirstTail
	}
	lines := make(chan docker.LogLine)
	done := make(chan error, 1)
	go func() {
		done <- c.Logs(ctx, id, o, func(l docker.LogLine) {
			select {
			case lines <- l:
			case <-ctx.Done():
			}
		})
	}()

	tick := time.NewTicker(logFlushEvery)
	defer tick.Stop()
	flush := func() {
		if out := b.flush(); len(out) > 0 && ctx.Err() == nil {
			publish(logLines{Lines: out})
		}
	}
	got := false
	for {
		select {
		case l := <-lines:
			if b.add(l) {
				got = true
			}
			if b.full() {
				flush()
			}
		case <-tick.C:
			flush()
		case err := <-done:
			// lines não tem buffer: toda linha enviada já foi recebida.
			flush()
			return got, err
		}
	}
}

// ContainerStatsSource publica cada docker.Stats do container do tópico
// "container-stats:<id>". Quando o stream termina, publica {"running":false}
// (uma vez até chegarem amostras novas) e tenta de novo a cada retry.
func ContainerStatsSource(c docker.Client, retry time.Duration) events.SourceFunc {
	return func(ctx context.Context, topic string, publish func(any)) {
		id, ok := topicContainerID(topic)
		if !ok {
			publish(sourceError{Error: "INVALID_CONTAINER_ID"})
			return
		}
		stopped := false
		for {
			var got atomic.Bool
			err := c.Stats(ctx, id, func(s docker.Stats) {
				got.Store(true)
				publish(s)
			})
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, docker.ErrNotFound) {
				publish(sourceError{Error: "CONTAINER_NOT_FOUND"})
				return
			}
			if got.Load() {
				stopped = false
			}
			if !stopped {
				publish(statsStopped{Running: false})
				stopped = true
			}
			if !sleepCtx(ctx, retry) {
				return
			}
		}
	}
}
