package deploy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/engviniciusbs/korego/internal/events"
)

// maxLogLine limita o tamanho de uma linha do log; o excedente vira outra linha.
const maxLogLine = 16 * 1024

// LogEntry é uma linha numerada do log de um deploy (a partir de 1).
type LogEntry struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// LogTopic é o tópico do hub com as linhas do log do deploy id.
func LogTopic(id int64) string { return "deploy-logs:" + strconv.FormatInt(id, 10) }

func logPath(dataDir string, id int64) string {
	return filepath.Join(dataDir, "logs", fmt.Sprintf("deploy-%d.log", id))
}

// DeployLog grava as linhas do log de um deploy em <dataDir>/logs/deploy-<id>.log
// e as publica no tópico deploy-logs:<id>. O texto já chega mascarado.
type DeployLog struct {
	mu    sync.Mutex
	f     *os.File
	n     int
	hub   *events.Hub
	topic string
}

// OpenDeployLog abre (em append) o log do deploy, criando logs/ com 0700.
// A numeração continua depois das linhas que já existem.
func OpenDeployLog(dataDir string, id int64, hub *events.Hub) (*DeployLog, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o700); err != nil {
		return nil, fmt.Errorf("criar diretório de logs: %w", err)
	}
	p := logPath(dataDir, id)
	existing, err := ReadLog(dataDir, id, 0)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("abrir log do deploy: %w", err)
	}
	return &DeployLog{f: f, n: len(existing), hub: hub, topic: LogTopic(id)}, nil
}

// Line grava uma linha (sem '\n') e a publica com o número seguinte.
func (l *DeployLog) Line(text string) error {
	text = strings.ToValidUTF8(text, "�")
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return errors.New("log do deploy fechado")
	}
	if _, err := l.f.WriteString(text + "\n"); err != nil {
		return fmt.Errorf("gravar log do deploy: %w", err)
	}
	l.n++
	if l.hub != nil {
		l.hub.Publish(l.topic, LogEntry{N: l.n, Text: text})
	}
	return nil
}

// Close fecha o arquivo. Pode ser chamado mais de uma vez.
func (l *DeployLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// ReadLog devolve as linhas do log com número maior que from. Log inexistente
// devolve lista vazia.
func ReadLog(dataDir string, id int64, from int) ([]LogEntry, error) {
	out := []LogEntry{}
	f, err := os.Open(logPath(dataDir, id))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler log do deploy: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*maxLogLine)
	n := 0
	for sc.Scan() {
		n++
		if n > from {
			out = append(out, LogEntry{N: n, Text: sc.Text()})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("ler log do deploy: %w", err)
	}
	return out, nil
}

// lineSplitter recebe a saída já mascarada e entrega ao DeployLog uma linha por
// '\n' (sem '\r' final). Linhas maiores que maxLogLine são quebradas. Falhas de
// gravação do log não interrompem o deploy: são registradas e ignoradas.
type lineSplitter struct {
	mu     sync.Mutex
	dst    *DeployLog
	buf    []byte
	warned bool
}

func (s *lineSplitter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			s.buf = append(s.buf, p...)
			for len(s.buf) >= maxLogLine {
				s.emit(s.buf[:maxLogLine])
				s.buf = append(s.buf[:0], s.buf[maxLogLine:]...)
			}
			break
		}
		s.buf = append(s.buf, p[:i]...)
		s.emit(s.buf)
		s.buf = s.buf[:0]
		p = p[i+1:]
	}
	return n, nil
}

// Flush entrega a linha incompleta, se houver.
func (s *lineSplitter) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) > 0 {
		s.emit(s.buf)
		s.buf = s.buf[:0]
	}
}

func (s *lineSplitter) emit(b []byte) {
	if err := s.dst.Line(string(bytes.TrimSuffix(b, []byte("\r")))); err != nil && !s.warned {
		s.warned = true
		log.Printf("deploy: %v", err)
	}
}
