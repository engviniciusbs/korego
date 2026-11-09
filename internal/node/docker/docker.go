// Package docker define o cliente Docker usado pelo KoreGO: uma interface
// própria sobre o SDK oficial, com tipos simples para a API.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Erros devolvidos pelo Client (use errors.Is).
var (
	ErrUnavailable = errors.New("docker indisponível")
	ErrNotFound    = errors.New("container não encontrado")
	ErrConflict    = errors.New("conflito no docker")
)

// Port é uma porta publicada (ou só exposta, com PublicPort 0).
type Port struct {
	IP          string `json:"ip"`
	PrivatePort uint16 `json:"privatePort"`
	PublicPort  uint16 `json:"publicPort"`
	Proto       string `json:"proto"`
}

// Container é o resumo de um container, como na listagem.
type Container struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"` // sem a "/" inicial
	Image     string    `json:"image"`
	State     string    `json:"state"`  // running, exited, paused, restarting, created, dead
	Status    string    `json:"status"` // texto do Docker, ex.: "Up 3 hours"
	Health    string    `json:"health"` // healthy, unhealthy, starting ou ""
	CreatedAt time.Time `json:"createdAt"`
	Project   string    `json:"project"` // label com.docker.compose.project
	Service   string    `json:"service"` // label com.docker.compose.service
	Managed   bool      `json:"managed"` // label korego.managed == "true"
	Ports     []Port    `json:"ports"`
	// Labels são os labels do container (só uso interno, não vai à API).
	Labels map[string]string `json:"-"`
}

// Mount é um volume ou bind montado no container.
type Mount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadWrite   bool   `json:"readWrite"`
}

// Details são os detalhes de um container (inspect).
type Details struct {
	Container
	Command      string    `json:"command"`
	StartedAt    time.Time `json:"startedAt"`
	RestartCount int       `json:"restartCount"`
	EnvNames     []string  `json:"envNames"` // só os nomes, em ordem alfabética
	Networks     []string  `json:"networks"`
	Mounts       []Mount   `json:"mounts"`
	Pid          int       `json:"pid"`      // PID do processo principal no host (0 se parado)
	ExitCode     int       `json:"exitCode"` // código de saída da última execução (State.ExitCode)
}

// LogLine é uma linha de log de um container.
type LogLine struct {
	TS     string `json:"ts"`     // unix nanos com 19 dígitos, comparável como texto
	Stream string `json:"stream"` // stdout | stderr
	Text   string `json:"text"`   // UTF-8 válido, no máximo 16 KiB (o resto é cortado com "…")
}

// Stats é uma amostra de uso de recursos de um container.
type Stats struct {
	CPUPercent    float64 `json:"cpuPercent"`
	MemUsedBytes  uint64  `json:"memUsedBytes"`
	MemLimitBytes uint64  `json:"memLimitBytes"`
}

// Event é um evento de container.
type Event struct {
	Action string `json:"action"` // start, stop, die, ...; health_status sem o sufixo ": healthy"
	ID     string `json:"id"`
	Name   string `json:"name"`
}

// LogOptions controla a leitura de logs.
type LogOptions struct {
	Tail   int    // 0 = todas
	Since  string // TS da última linha já vista ("" = sem filtro)
	Follow bool
}

// Client é o acesso ao Docker de um node.
type Client interface {
	Ping(ctx context.Context) error
	List(ctx context.Context) ([]Container, error) // all=true
	Inspect(ctx context.Context, id string) (Details, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error // timeout padrão do container
	Restart(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error                               // sem force, sem apagar volumes
	Logs(ctx context.Context, id string, o LogOptions, fn func(LogLine)) error // bloqueia até o fim do stream ou de ctx
	Stats(ctx context.Context, id string, fn func(Stats)) error                // stream; bloqueia
	Events(ctx context.Context, fn func(Event)) error                          // filtro type=container e as ações publicadas; bloqueia

	Pull(ctx context.Context, ref string, fn func(line string)) error // fn recebe o progresso em texto ("<id>: <status> <progresso>")
	Run(ctx context.Context, s RunSpec) (id string, err error)        // create + start; ErrConflict se o nome já existe
	RemoveForce(ctx context.Context, id string) error                 // force, sem apagar volumes; ErrNotFound → nil
	Images(ctx context.Context, repo string) ([]Image, error)         // imagens com tag em repo (ex.: "korego/blog"), mais nova primeiro
	RemoveImage(ctx context.Context, ref string) error                // sem force; em uso → ErrConflict
	ListByLabel(ctx context.Context, key, value string) ([]Container, error)
}

// EventActions são as ações de container repassadas por Events.
var EventActions = []string{"create", "start", "stop", "die", "destroy", "rename", "pause", "unpause", "health_status"}

// maxLineBytes é o tamanho máximo do texto de uma linha de log.
const maxLineBytes = 16 * 1024

// maxBuffered limita quanto de uma linha sem '\n' fica em memória
// (texto máximo + prefixo de timestamp + folga).
const maxBuffered = maxLineBytes + 128

// sanitizeLine devolve b como UTF-8 válido (bytes inválidos viram U+FFFD),
// cortado em no máximo 16 KiB num limite de rune, com "…" quando cortado.
func sanitizeLine(b []byte) string {
	s := strings.ToValidUTF8(string(b), "�")
	if len(s) <= maxLineBytes {
		return s
	}
	cut := maxLineBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// formatTS formata t como unix nanos com 19 dígitos.
func formatTS(t time.Time) string { return fmt.Sprintf("%019d", t.UnixNano()) }

// parseLogLine separa o timestamp RFC3339Nano que o Docker põe no início da
// linha. Sem timestamp válido, usa lastTS e mantém a linha inteira.
func parseLogLine(b []byte, stream, lastTS string) LogLine {
	b = bytes.TrimSuffix(b, []byte("\r"))
	ts := lastTS
	if i := bytes.IndexByte(b, ' '); i > 0 {
		if t, err := time.Parse(time.RFC3339Nano, string(b[:i])); err == nil {
			ts = formatTS(t)
			b = b[i+1:]
		}
	}
	return LogLine{TS: ts, Stream: stream, Text: sanitizeLine(b)}
}

// sinceParam converte o TS da última linha vista no formato "<seg>.<nanos>"
// do Docker, somando 1 ns para não repetir essa linha (o filtro é inclusivo).
func sinceParam(ts string) (string, error) {
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || n < 0 {
		return "", fmt.Errorf("since inválido: %q", ts)
	}
	n++
	return fmt.Sprintf("%d.%09d", n/1e9, n%1e9), nil
}

// lineWriter recebe bytes de um stream e chama fn a cada linha completa.
type lineWriter struct {
	stream string
	fn     func(LogLine)
	buf    []byte
	lastTS string
}

func newLineWriter(stream string, fn func(LogLine)) *lineWriter {
	return &lineWriter{stream: stream, fn: fn}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if room := maxBuffered - len(w.buf); room > 0 {
			w.buf = append(w.buf, chunk[:min(room, len(chunk))]...)
		}
		if i < 0 {
			break
		}
		w.emit()
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) emit() {
	l := parseLogLine(w.buf, w.stream, w.lastTS)
	w.lastTS = l.TS
	w.buf = w.buf[:0]
	w.fn(l)
}

// flush emite a última linha, se ela não terminou com '\n'.
func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.emit()
	}
}

// PortMapping publica uma porta do container no host.
type PortMapping struct {
	HostIP        string // "" = todas as interfaces
	HostPort      uint16 // 0 = o Docker escolhe
	ContainerPort uint16
	Proto         string // "tcp" por padrão
}

// VolumeMount monta um volume nomeado em Target; o Docker o cria no primeiro uso.
type VolumeMount struct{ Volume, Target string }

// BindMount monta um caminho do host em Target.
type BindMount struct {
	Source, Target string
	ReadOnly       bool
}

// RunSpec descreve um container a criar e iniciar.
type RunSpec struct {
	Name, Image   string
	Labels        map[string]string
	Env           []string // "NOME=valor"; nunca registrado em log nem em erro
	Ports         []PortMapping
	Volumes       []VolumeMount
	Binds         []BindMount
	Cmd           []string
	NetworkMode   string // "" = padrão; "host" para o Caddy
	RestartPolicy string // "unless-stopped" por padrão
}

// Image é uma imagem local, vista por uma de suas tags.
type Image struct {
	ID        string
	Ref       string // primeira tag do repositório consultado, ex.: "korego/blog:abc123"
	CreatedAt time.Time
}
