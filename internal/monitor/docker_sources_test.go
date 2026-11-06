package monitor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/monitor"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
)

const validID = "abcdef123456"

// runSource executa a fonte em uma goroutine e devolve o canal com o que foi
// publicado (já em JSON) e uma função que cancela e espera a fonte terminar.
func runSource(t *testing.T, fn events.SourceFunc, topic string) (<-chan string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan string, 1024)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(ctx, topic, func(data any) {
			b, err := json.Marshal(data)
			if err != nil {
				panic(err)
			}
			out <- string(b)
		})
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("a fonte não terminou após o cancelamento")
		}
	}
	t.Cleanup(stop)
	return out, stop
}

// next espera a próxima mensagem publicada.
func next(t *testing.T, out <-chan string) string {
	t.Helper()
	select {
	case m := <-out:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("nenhuma mensagem publicada")
		return ""
	}
}

func ts(n int) string { return fmt.Sprintf("%019d", n) }

func TestEventsSource_PublishesAndReconnects(t *testing.T) {
	var calls atomic.Int32
	fake := &dockertest.Fake{
		PingFn: func(context.Context) error { return nil },
		EventsFn: func(ctx context.Context, fn func(docker.Event)) error {
			if calls.Add(1) == 1 {
				return docker.ErrUnavailable
			}
			fn(docker.Event{Action: "start", ID: validID, Name: "web"})
			<-ctx.Done()
			return ctx.Err()
		},
	}
	out, _ := runSource(t, monitor.ContainerEventsSource(fake, time.Millisecond), "containers")
	assert.JSONEq(t, `{"action":"unavailable"}`, next(t, out))
	assert.JSONEq(t, `{"action":"reconnected"}`, next(t, out))
	assert.JSONEq(t, `{"action":"start","id":"abcdef123456","name":"web"}`, next(t, out))
}

func TestEventsSource_StaysUnavailableWhilePingFails(t *testing.T) {
	var pings atomic.Int32
	fake := &dockertest.Fake{
		PingFn: func(context.Context) error {
			if pings.Add(1) < 3 {
				return docker.ErrUnavailable
			}
			return nil
		},
		EventsFn: func(ctx context.Context, fn func(docker.Event)) error {
			if pings.Load() == 0 {
				return docker.ErrUnavailable
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	out, _ := runSource(t, monitor.ContainerEventsSource(fake, time.Millisecond), "containers")
	assert.JSONEq(t, `{"action":"unavailable"}`, next(t, out))
	assert.JSONEq(t, `{"action":"reconnected"}`, next(t, out))
	assert.GreaterOrEqual(t, pings.Load(), int32(3))
}

// lineBatches lê lotes {"lines":[...]} até juntar n linhas.
func lineBatches(t *testing.T, out <-chan string, n int) (batches [][]docker.LogLine, all []docker.LogLine) {
	t.Helper()
	for len(all) < n {
		var msg struct {
			Lines []docker.LogLine `json:"lines"`
		}
		raw := next(t, out)
		require.NoError(t, json.Unmarshal([]byte(raw), &msg))
		require.NotEmpty(t, msg.Lines, "mensagem inesperada: %s", raw)
		batches = append(batches, msg.Lines)
		all = append(all, msg.Lines...)
	}
	return batches, all
}

func TestLogsSource_BatchesLines(t *testing.T) {
	const total = 450
	fake := &dockertest.Fake{LogsFn: func(ctx context.Context, _ string, _ docker.LogOptions, fn func(docker.LogLine)) error {
		for i := 1; i <= total; i++ {
			fn(docker.LogLine{TS: ts(2e18 + i), Stream: "stdout", Text: fmt.Sprint(i)})
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	out, _ := runSource(t, monitor.ContainerLogsSource(fake, time.Millisecond), "container-logs:"+validID)
	batches, all := lineBatches(t, out, total)
	require.Len(t, all, total)
	for _, b := range batches {
		assert.LessOrEqual(t, len(b), 200)
	}
	for i, l := range all {
		assert.Equal(t, fmt.Sprint(i+1), l.Text)
	}
}

func TestLogsSource_ResumesWithoutDuplicates(t *testing.T) {
	var mu sync.Mutex
	var sinces []string
	fake := &dockertest.Fake{InspectFn: running, LogsFn: func(ctx context.Context, _ string, o docker.LogOptions, fn func(docker.LogLine)) error {
		mu.Lock()
		sinces = append(sinces, o.Since)
		call := len(sinces)
		mu.Unlock()
		base := 2e18
		switch call {
		case 1:
			for i := 1; i <= 3; i++ {
				fn(docker.LogLine{TS: ts(int(base) + i), Text: fmt.Sprint(i)})
			}
			return nil
		case 2:
			for i := 3; i <= 5; i++ {
				fn(docker.LogLine{TS: ts(int(base) + i), Text: fmt.Sprint(i)})
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	out, _ := runSource(t, monitor.ContainerLogsSource(fake, time.Millisecond), "container-logs:"+validID)

	var texts []string
	ended := 0
	for len(texts) < 5 {
		raw := next(t, out)
		if raw == `{"ended":true}` {
			ended++
			continue
		}
		var msg struct {
			Lines []docker.LogLine `json:"lines"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &msg), raw)
		require.NotEmpty(t, msg.Lines, "mensagem inesperada: %s", raw)
		for _, l := range msg.Lines {
			texts = append(texts, l.Text)
		}
	}
	assert.Equal(t, []string{"1", "2", "3", "4", "5"}, texts)
	assert.Equal(t, 1, ended)
	select {
	case m := <-out:
		t.Fatalf("mensagem a mais: %s", m)
	case <-time.After(150 * time.Millisecond):
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, sinces, 2)
	assert.Empty(t, sinces[0])
	assert.Equal(t, ts(2e18+3), sinces[1])
}

// running é um InspectFn de container rodando.
func running(context.Context, string) (docker.Details, error) {
	return docker.Details{Container: docker.Container{ID: validID, State: "running"}}, nil
}

func TestLogsSource_FirstStreamTailThenSince(t *testing.T) {
	opts := make(chan docker.LogOptions, 2)
	var calls atomic.Int32
	fake := &dockertest.Fake{InspectFn: running, LogsFn: func(ctx context.Context, _ string, o docker.LogOptions, fn func(docker.LogLine)) error {
		opts <- o
		if calls.Add(1) == 1 {
			fn(docker.LogLine{TS: ts(2e18 + 7), Text: "a"})
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	runSource(t, monitor.ContainerLogsSource(fake, time.Millisecond), "container-logs:"+validID)
	recv := func() docker.LogOptions {
		select {
		case o := <-opts:
			return o
		case <-time.After(2 * time.Second):
			t.Fatal("Logs não foi chamado")
			return docker.LogOptions{}
		}
	}
	// Primeiro stream: tail curto, sem Since (sem varrer o arquivo de log).
	assert.Equal(t, docker.LogOptions{Tail: 200, Follow: true}, recv())
	// Retry: retoma da última linha publicada.
	assert.Equal(t, docker.LogOptions{Since: ts(2e18 + 7), Follow: true}, recv())
}

func TestLogsSource_WaitsWhileStopped(t *testing.T) {
	var state atomic.Value
	state.Store("exited")
	var inspects, logs atomic.Int32
	sinces := make(chan string, 4)
	fake := &dockertest.Fake{
		InspectFn: func(context.Context, string) (docker.Details, error) {
			inspects.Add(1)
			return docker.Details{Container: docker.Container{ID: validID, State: state.Load().(string)}}, nil
		},
		LogsFn: func(ctx context.Context, _ string, o docker.LogOptions, fn func(docker.LogLine)) error {
			sinces <- o.Since
			if logs.Add(1) == 1 {
				fn(docker.LogLine{TS: ts(2e18 + 1), Text: "a"})
				return nil // container parou
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	runSource(t, monitor.ContainerLogsSource(fake, time.Millisecond), "container-logs:"+validID)
	require.Equal(t, "", <-sinces)

	// Parado: só Inspect a cada retry, Logs não é chamado de novo.
	require.Eventually(t, func() bool { return inspects.Load() >= 5 }, 2*time.Second, time.Millisecond)
	assert.Equal(t, int32(1), logs.Load())

	state.Store("running")
	select {
	case s := <-sinces:
		assert.Equal(t, ts(2e18+1), s)
	case <-time.After(2 * time.Second):
		t.Fatal("Logs não foi chamado com o container rodando")
	}
}

func TestLogsSource_RemovedWhileStopped(t *testing.T) {
	fake := &dockertest.Fake{
		InspectFn: func(context.Context, string) (docker.Details, error) { return docker.Details{}, docker.ErrNotFound },
		LogsFn:    func(context.Context, string, docker.LogOptions, func(docker.LogLine)) error { return nil },
	}
	var msgs []string
	// A fonte encerra sozinha quando o Inspect do retry não acha o container.
	monitor.ContainerLogsSource(fake, time.Millisecond)(context.Background(), "container-logs:"+validID, func(d any) {
		b, _ := json.Marshal(d)
		msgs = append(msgs, string(b))
	})
	assert.Equal(t, []string{`{"ended":true}`, `{"error":"CONTAINER_NOT_FOUND"}`}, msgs)
}

func TestLogsSource_FlushesBeforeEnded(t *testing.T) {
	var calls atomic.Int32
	fake := &dockertest.Fake{LogsFn: func(ctx context.Context, _ string, _ docker.LogOptions, fn func(docker.LogLine)) error {
		if calls.Add(1) == 1 {
			fn(docker.LogLine{TS: ts(2e18 + 1), Text: "a"})
			return docker.ErrUnavailable // queda abrupta também encerra o stream
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	out, _ := runSource(t, monitor.ContainerLogsSource(fake, time.Millisecond), "container-logs:"+validID)
	assert.JSONEq(t, `{"lines":[{"ts":"2000000000000000001","stream":"","text":"a"}]}`, next(t, out))
	assert.JSONEq(t, `{"ended":true}`, next(t, out))
}

func TestLogsSource_InvalidAndMissing(t *testing.T) {
	var called atomic.Bool
	fake := &dockertest.Fake{LogsFn: func(context.Context, string, docker.LogOptions, func(docker.LogLine)) error {
		called.Store(true)
		return nil
	}}
	out, stop := runSource(t, monitor.ContainerLogsSource(fake, time.Millisecond), "container-logs:zz")
	assert.JSONEq(t, `{"error":"INVALID_CONTAINER_ID"}`, next(t, out))
	stop()
	assert.False(t, called.Load())

	missing := &dockertest.Fake{LogsFn: func(context.Context, string, docker.LogOptions, func(docker.LogLine)) error {
		return docker.ErrNotFound
	}}
	ctx := context.Background()
	var msgs []string
	// A fonte encerra sozinha: a chamada volta sem cancelar o ctx.
	monitor.ContainerLogsSource(missing, time.Millisecond)(ctx, "container-logs:"+validID, func(d any) {
		b, _ := json.Marshal(d)
		msgs = append(msgs, string(b))
	})
	assert.Equal(t, []string{`{"error":"CONTAINER_NOT_FOUND"}`}, msgs)
}

func TestStatsSource_StoppedContainer(t *testing.T) {
	var calls atomic.Int32
	fake := &dockertest.Fake{StatsFn: func(ctx context.Context, id string, fn func(docker.Stats)) error {
		assert.Equal(t, validID, id)
		if calls.Add(1) == 1 {
			fn(docker.Stats{CPUPercent: 1.5, MemUsedBytes: 10, MemLimitBytes: 100})
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	out, _ := runSource(t, monitor.ContainerStatsSource(fake, time.Millisecond), "container-stats:"+validID)
	assert.JSONEq(t, `{"cpuPercent":1.5,"memUsedBytes":10,"memLimitBytes":100}`, next(t, out))
	assert.JSONEq(t, `{"running":false}`, next(t, out))
	require.Eventually(t, func() bool { return calls.Load() >= 2 }, time.Second, time.Millisecond)
}

func TestStatsSource_InvalidAndMissing(t *testing.T) {
	ctx := context.Background()
	collect := func(fn events.SourceFunc, topic string) []string {
		var msgs []string
		fn(ctx, topic, func(d any) {
			b, _ := json.Marshal(d)
			msgs = append(msgs, string(b))
		})
		return msgs
	}
	fake := &dockertest.Fake{StatsFn: func(context.Context, string, func(docker.Stats)) error { return docker.ErrNotFound }}
	assert.Equal(t, []string{`{"error":"INVALID_CONTAINER_ID"}`},
		collect(monitor.ContainerStatsSource(fake, time.Millisecond), "container-stats:ABCDEF123456"))
	assert.Equal(t, []string{`{"error":"CONTAINER_NOT_FOUND"}`},
		collect(monitor.ContainerStatsSource(fake, time.Millisecond), "container-stats:"+validID))
}

func TestSources_StopWithLastSubscriber(t *testing.T) {
	// block registra o ctx recebido pelo fake e espera o cancelamento.
	type probe struct{ ctxs chan context.Context }
	newProbe := func() probe { return probe{ctxs: make(chan context.Context, 1)} }
	block := func(p probe, ctx context.Context) error {
		select {
		case p.ctxs <- ctx:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	pe, pl, ps := newProbe(), newProbe(), newProbe()
	fake := &dockertest.Fake{
		EventsFn: func(ctx context.Context, _ func(docker.Event)) error { return block(pe, ctx) },
		LogsFn: func(ctx context.Context, _ string, _ docker.LogOptions, _ func(docker.LogLine)) error {
			return block(pl, ctx)
		},
		StatsFn: func(ctx context.Context, _ string, _ func(docker.Stats)) error { return block(ps, ctx) },
	}
	hub := events.NewHub()
	hub.RegisterSource("containers", monitor.ContainerEventsSource(fake, time.Millisecond))
	hub.RegisterSource("container-logs", monitor.ContainerLogsSource(fake, time.Millisecond))
	hub.RegisterSource("container-stats", monitor.ContainerStatsSource(fake, time.Millisecond))

	for _, c := range []struct {
		topic string
		p     probe
	}{
		{"containers", pe},
		{"container-logs:" + validID, pl},
		{"container-stats:" + validID, ps},
	} {
		_, cancel1 := hub.Subscribe(c.topic)
		_, cancel2 := hub.Subscribe(c.topic)
		var ctx context.Context
		select {
		case ctx = <-c.p.ctxs:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: fonte não chamou o docker", c.topic)
		}
		cancel1()
		assert.NoError(t, ctx.Err(), "%s: ctx cancelado com assinante restante", c.topic)
		cancel2()
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: ctx não foi cancelado com o último assinante", c.topic)
		}
	}
}

func TestSources_ReturnWhileWaitingRetry(t *testing.T) {
	fake := &dockertest.Fake{
		LogsFn:  func(context.Context, string, docker.LogOptions, func(docker.LogLine)) error { return nil },
		StatsFn: func(context.Context, string, func(docker.Stats)) error { return nil },
	}
	// retry longo: a fonte fica esperando e precisa voltar logo ao cancelar.
	for _, c := range []struct {
		fn    events.SourceFunc
		topic string
	}{
		{monitor.ContainerEventsSource(fake, time.Hour), "containers"},
		{monitor.ContainerLogsSource(fake, time.Hour), "container-logs:" + validID},
		{monitor.ContainerStatsSource(fake, time.Hour), "container-stats:" + validID},
	} {
		out, stop := runSource(t, c.fn, c.topic)
		next(t, out) // unavailable / ended / running:false
		stop()
	}
}
