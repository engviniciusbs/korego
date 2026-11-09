// Package certs guarda certificados TLS próprios (cadeia e chave) cifrados e
// valida o que o usuário cola ou aponta no disco.
package certs

import (
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

// certError carrega uma mensagem pronta para o usuário e casa com um sentinela via errors.Is.
type certError struct {
	base error
	msg  string
}

func (e *certError) Error() string        { return e.msg }
func (e *certError) Is(target error) bool { return target == e.base }

var (
	// ErrInvalidCert: o texto não é uma cadeia + chave utilizável.
	ErrInvalidCert = errors.New("certificado inválido")
	// ErrKeyMismatch: nenhuma chave combina com algum certificado da cadeia.
	ErrKeyMismatch = errors.New("A chave privada não corresponde ao certificado.")
	// ErrExpired: o certificado folha já venceu.
	ErrExpired = errors.New("O certificado está expirado.")
	// ErrDuplicate: já existe um certificado com o mesmo fingerprint.
	ErrDuplicate = errors.New("Este certificado já está cadastrado.")
)

func invalid(msg string) error { return &certError{base: ErrInvalidCert, msg: msg} }

// readFailMsg é a única mensagem para qualquer falha de leitura; o erro do SO
// nunca chega ao usuário.
const readFailMsg = "Não foi possível ler o arquivo informado."

var errReadFile = errors.New(readFailMsg)

// maxFileBytes é o limite de leitura do formulário "path".
const maxFileBytes = 1 << 20

// ExpiresSoonWindow é a antecedência do aviso de vencimento.
const ExpiresSoonWindow = 30 * 24 * time.Hour

// Info resume o certificado folha.
type Info struct {
	Issuer      string
	Names       []string
	NotAfter    time.Time
	Fingerprint string // SHA-256 do DER da folha, hex maiúsculo separado por ":"
	// PEM é o texto normalizado a guardar: folha, demais certificados na ordem
	// original e, por último, a chave. Nunca vai para a API.
	PEM []byte
}

// Parse valida cadeia e chave coladas em qualquer ordem num único texto.
// A folha é o certificado cuja chave pública combina com a chave privada.
func Parse(in []byte, now time.Time) (Info, error) {
	var chain []*x509.Certificate
	var keyDER []byte
	var keyType string
	rest := in
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		switch {
		case b.Type == "CERTIFICATE":
			c, err := x509.ParseCertificate(b.Bytes)
			if err != nil {
				return Info{}, invalid("Um dos certificados do PEM não pôde ser lido.")
			}
			chain = append(chain, c)
		case b.Type == "ENCRYPTED PRIVATE KEY" || b.Headers["Proc-Type"] == "4,ENCRYPTED" ||
			strings.Contains(b.Headers["Proc-Type"], "ENCRYPTED"):
			return Info{}, invalid("A chave privada está cifrada com senha; remova a senha antes de enviar.")
		case strings.HasSuffix(b.Type, "PRIVATE KEY"):
			if keyDER != nil {
				return Info{}, invalid("O PEM contém mais de uma chave privada.")
			}
			keyDER, keyType = b.Bytes, b.Type
		}
	}
	if len(chain) == 0 {
		return Info{}, invalid("Nenhum certificado encontrado no PEM.")
	}
	if keyDER == nil {
		return Info{}, invalid("Nenhuma chave privada encontrada no PEM.")
	}
	signer, err := parseKey(keyType, keyDER)
	if err != nil {
		return Info{}, invalid("A chave privada não pôde ser lida (use RSA, PKCS#8 ou EC).")
	}
	leafIdx := -1
	for i, c := range chain {
		if pubEqual(c.PublicKey, signer.Public()) {
			leafIdx = i
			break
		}
	}
	if leafIdx < 0 {
		return Info{}, ErrKeyMismatch
	}
	leaf := chain[leafIdx]
	if !leaf.NotAfter.After(now) {
		return Info{}, ErrExpired
	}

	sum := sha256.Sum256(leaf.Raw)
	pairs := make([]string, len(sum))
	for i, v := range sum {
		pairs[i] = fmt.Sprintf("%02X", v)
	}

	var norm []byte
	norm = append(norm, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})...)
	for i, c := range chain {
		if i != leafIdx {
			norm = append(norm, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
		}
	}
	norm = append(norm, pem.EncodeToMemory(&pem.Block{Type: keyType, Bytes: keyDER})...)

	return Info{
		Issuer: issuerName(leaf), Names: leafNames(leaf), NotAfter: leaf.NotAfter,
		Fingerprint: strings.Join(pairs, ":"), PEM: norm,
	}, nil
}

func parseKey(typ string, der []byte) (crypto.Signer, error) {
	switch typ {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(der)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(der)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return nil, err
		}
		s, ok := k.(crypto.Signer)
		if !ok {
			return nil, errors.New("tipo de chave não suportado")
		}
		return s, nil
	}
	return nil, errors.New("tipo de chave não suportado")
}

func pubEqual(a, b crypto.PublicKey) bool {
	eq, ok := a.(interface{ Equal(crypto.PublicKey) bool })
	return ok && eq.Equal(b)
}

func issuerName(c *x509.Certificate) string {
	if c.Issuer.CommonName != "" {
		return c.Issuer.CommonName
	}
	return c.Issuer.String()
}

// leafNames devolve os nomes DNS (minúsculos); sem SAN, cai no CN.
func leafNames(c *x509.Certificate) []string {
	out := []string{}
	for _, n := range c.DNSNames {
		out = append(out, strings.ToLower(n))
	}
	if len(out) == 0 && c.Subject.CommonName != "" {
		out = append(out, strings.ToLower(c.Subject.CommonName))
	}
	return out
}

// Covers diz se algum nome cobre host, sem diferenciar maiúsculas. Um curinga
// "*.x" cobre exatamente um rótulo à esquerda de x.
func Covers(names []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSuffix(n, "."))
		if n == host {
			return true
		}
		if suffix, ok := strings.CutPrefix(n, "*"); ok && strings.HasPrefix(suffix, ".") {
			label, ok := strings.CutSuffix(host, suffix)
			if ok && label != "" && !strings.Contains(label, ".") {
				return true
			}
		}
	}
	return false
}

// Service cadastra, lista e apaga certificados próprios.
type Service struct {
	DB  *store.DB
	Box *secrets.Box
	Now func() time.Time // nil = time.Now
}

// View é o certificado sem o PEM, para a API.
type View struct {
	store.Certificate
	UsedBy      []string
	ExpiresSoon bool
}

// ViewOf monta a View de um certificado já lido.
func (s *Service) ViewOf(c store.Certificate, usedBy []string) View {
	return View{Certificate: c, UsedBy: usedBy, ExpiresSoon: c.NotAfter.Sub(s.now()) <= ExpiresSoonWindow}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Add valida o PEM e grava o certificado cifrado.
func (s *Service) Add(ctx context.Context, name string, pemText []byte) (store.Certificate, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return store.Certificate{}, invalid("Informe um nome de até 100 caracteres.")
	}
	info, err := Parse(pemText, s.now())
	if err != nil {
		return store.Certificate{}, err
	}
	enc, err := s.Box.Encrypt(info.PEM)
	if err != nil {
		return store.Certificate{}, fmt.Errorf("cifrar certificado: %w", err)
	}
	c := store.Certificate{
		Name: name, Issuer: info.Issuer, Names: info.Names, NotAfter: info.NotAfter,
		Fingerprint: info.Fingerprint, PEMEnc: enc,
	}
	id, err := s.DB.CreateCertificate(ctx, c)
	if errors.Is(err, store.ErrConflict) {
		return store.Certificate{}, ErrDuplicate
	}
	if err != nil {
		return store.Certificate{}, err
	}
	return s.DB.CertificateByID(ctx, id)
}

// AddFromPath lê um arquivo PEM (caminho absoluto, até 1 MB) e o cadastra.
// Nunca devolve o conteúdo do arquivo em mensagens.
func (s *Service) AddFromPath(ctx context.Context, name, path string) (store.Certificate, error) {
	data, err := readLimited(path)
	if err != nil {
		return store.Certificate{}, invalid(readFailMsg)
	}
	return s.Add(ctx, name, data)
}

func readLimited(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errReadFile
	}
	// Stat antes de abrir: abrir um FIFO para leitura travaria.
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		return nil, errReadFile
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errReadFile
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, errReadFile
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil || len(data) > maxFileBytes {
		return nil, errReadFile
	}
	return data, nil
}

// Get devolve um certificado (com o PEM cifrado) ou store.ErrNotFound.
func (s *Service) Get(ctx context.Context, id int64) (store.Certificate, error) {
	return s.DB.CertificateByID(ctx, id)
}

// List devolve os certificados com os domínios que os usam.
func (s *Service) List(ctx context.Context) ([]View, error) {
	list, err := s.DB.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(list))
	for _, c := range list {
		used, err := s.DB.DomainsUsingCert(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, s.ViewOf(c, used))
	}
	return out, nil
}

// Delete apaga o certificado; em uso → store.ErrCertInUse.
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.DB.DeleteCertificate(ctx, id)
}

// PEM decifra cadeia e chave. Só o serviço de domínios deve chamar isto.
func (s *Service) PEM(ctx context.Context, id int64) (string, error) {
	c, err := s.DB.CertificateByID(ctx, id)
	if err != nil {
		return "", err
	}
	plain, err := s.Box.Decrypt(c.PEMEnc)
	if err != nil {
		return "", fmt.Errorf("decifrar certificado: %w", err)
	}
	return string(plain), nil
}
