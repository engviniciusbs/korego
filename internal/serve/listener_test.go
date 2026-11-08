package serve

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startServer serve "ok" no Listener e devolve um canal com o erro de Serve.
func startServer(t *testing.T, l *Listener) <-chan error {
	t.Helper()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()
	t.Cleanup(func() {
		_ = srv.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve não terminou")
		}
	})
	return done
}

func get(t *testing.T, addr string) error {
	t.Helper()
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := c.Get("http://" + addr + "/")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" {
		return errors.New("corpo inesperado: " + string(b))
	}
	return nil
}

// freePort devolve uma porta TCP livre em 127.0.0.1.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, ln.Close())
	return port
}

func TestListener_SwitchKeepsServing(t *testing.T) {
	l, err := NewListener("127.0.0.1:0")
	require.NoError(t, err)
	done := startServer(t, l)
	oldAddr := l.Addr().String()
	require.NoError(t, get(t, oldAddr))

	// Conexão aberta antes da troca continua atendendo depois dela.
	kept, err := net.Dial("tcp", oldAddr)
	require.NoError(t, err)
	defer kept.Close()

	newAddr := "127.0.0.1:" + freePort(t)
	require.NoError(t, l.Switch(newAddr))
	assert.Equal(t, newAddr, l.Addr().String())
	require.NoError(t, get(t, newAddr))
	require.NoError(t, get(t, newAddr))

	_, err = io.WriteString(kept, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(kept), nil)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "ok", string(b))

	c, err := net.DialTimeout("tcp", oldAddr, 3*time.Second)
	if err == nil {
		c.Close()
	}
	assert.Error(t, err, "o endereço antigo não aceita mais conexões")

	// Trocar para o mesmo endereço não mexe no listener.
	require.NoError(t, l.Switch(newAddr))
	require.NoError(t, get(t, newAddr))

	select {
	case err := <-done:
		t.Fatalf("Serve terminou durante a troca: %v", err)
	default:
	}
}

func TestListener_SwitchFailureRestores(t *testing.T) {
	l, err := NewListener("127.0.0.1:0")
	require.NoError(t, err)
	startServer(t, l)
	oldAddr := l.Addr().String()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()

	err = l.Switch(busy.Addr().String())
	require.Error(t, err)
	assert.Equal(t, oldAddr, l.Addr().String())
	require.NoError(t, get(t, oldAddr))
	require.NoError(t, get(t, oldAddr))
}

// A troca do curinga para 127.0.0.1 na mesma porta exige fechar antes de abrir.
func TestListener_SwitchSamePortToLoopback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("escuta em todas as interfaces só no Linux (evita o firewall do Windows)")
	}
	port := freePort(t)
	l, err := NewListener(":" + port)
	require.NoError(t, err)
	startServer(t, l)
	require.NoError(t, get(t, "127.0.0.1:"+port))
	require.NoError(t, l.Switch("127.0.0.1:"+port))
	require.NoError(t, get(t, "127.0.0.1:"+port))
	require.NoError(t, l.Switch(":"+port))
	require.NoError(t, get(t, "127.0.0.1:"+port))
}

func TestListener_CloseEndsServe(t *testing.T) {
	l, err := NewListener("127.0.0.1:0")
	require.NoError(t, err)
	acc := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		acc <- err
	}()
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, l.Close())
	select {
	case err := <-acc:
		assert.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(3 * time.Second):
		t.Fatal("Accept não retornou após Close")
	}
	_, err = l.Accept()
	assert.ErrorIs(t, err, net.ErrClosed)
	assert.ErrorIs(t, l.Switch("127.0.0.1:0"), net.ErrClosed)
}

// Trocas repetidas com leituras de Addr em paralelo (rode com -race).
func TestListener_RepeatedSwitches(t *testing.T) {
	l, err := NewListener("127.0.0.1:0")
	require.NoError(t, err)
	done := startServer(t, l)
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				_ = l.Addr()
				time.Sleep(time.Millisecond)
			}
		}
	}()
	defer func() { close(stop); <-readerDone }()
	ports := []string{freePort(t), freePort(t)}
	for i := range 6 {
		addr := "127.0.0.1:" + ports[i%2]
		if err := l.Switch(addr); err != nil {
			// A porta livre pode ter sido tomada por outro processo.
			t.Logf("troca %d para %s: %v", i, addr, err)
			continue
		}
		require.NoError(t, get(t, l.Addr().String()), strconv.Itoa(i))
	}
	select {
	case err := <-done:
		t.Fatalf("Serve terminou: %v", err)
	default:
	}
}
