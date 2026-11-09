package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrCertInUse é devolvido ao apagar um certificado que algum domínio usa.
var ErrCertInUse = errors.New("certificado em uso por um domínio")

// Certificate é um certificado TLS próprio; a chave e a cadeia ficam cifradas em PEMEnc.
type Certificate struct {
	ID          int64
	Name        string
	Issuer      string
	Names       []string
	NotAfter    time.Time
	Fingerprint string
	PEMEnc      string
	CreatedAt   time.Time
}

const certCols = `id, name, issuer, names_json, not_after, fingerprint, pem_enc, created_at`

func scanCert(r rowScanner) (Certificate, error) {
	var c Certificate
	var namesJSON string
	var notAfter, created int64
	if err := r.Scan(&c.ID, &c.Name, &c.Issuer, &namesJSON, &notAfter, &c.Fingerprint, &c.PEMEnc, &created); err != nil {
		return Certificate{}, err
	}
	c.Names = []string{}
	if err := json.Unmarshal([]byte(namesJSON), &c.Names); err != nil {
		return Certificate{}, fmt.Errorf("nomes do certificado: %w", err)
	}
	c.NotAfter, c.CreatedAt = time.Unix(notAfter, 0), time.Unix(created, 0)
	return c, nil
}

// CreateCertificate grava o certificado e devolve o ID. Fingerprint repetido → ErrConflict.
func (d *DB) CreateCertificate(ctx context.Context, c Certificate) (int64, error) {
	names := c.Names
	if names == nil {
		names = []string{}
	}
	nj, err := json.Marshal(names)
	if err != nil {
		return 0, fmt.Errorf("gravar certificado: %w", err)
	}
	res, err := d.sql.ExecContext(ctx,
		`INSERT INTO certificates (name, issuer, names_json, not_after, fingerprint, pem_enc, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Issuer, string(nj), c.NotAfter.Unix(), c.Fingerprint, c.PEMEnc, time.Now().Unix())
	if err != nil {
		if isUnique(err) {
			return 0, fmt.Errorf("certificado %q: %w", c.Name, ErrConflict)
		}
		return 0, fmt.Errorf("gravar certificado: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("gravar certificado: %w", err)
	}
	return id, nil
}

// ListCertificates devolve todos os certificados ordenados por nome.
func (d *DB) ListCertificates(ctx context.Context) ([]Certificate, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+certCols+` FROM certificates ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("listar certificados: %w", err)
	}
	defer rows.Close()
	out := []Certificate{}
	for rows.Next() {
		c, err := scanCert(rows)
		if err != nil {
			return nil, fmt.Errorf("listar certificados: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar certificados: %w", err)
	}
	return out, nil
}

// CertificateByID devolve o certificado ou ErrNotFound.
func (d *DB) CertificateByID(ctx context.Context, id int64) (Certificate, error) {
	c, err := scanCert(d.sql.QueryRowContext(ctx, `SELECT `+certCols+` FROM certificates WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Certificate{}, ErrNotFound
	}
	if err != nil {
		return Certificate{}, fmt.Errorf("ler certificado: %w", err)
	}
	return c, nil
}

// DomainsUsingCert devolve os hostnames que usam o certificado, em ordem.
func (d *DB) DomainsUsingCert(ctx context.Context, id int64) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT hostname FROM domains WHERE cert_id = ? ORDER BY hostname`, id)
	if err != nil {
		return nil, fmt.Errorf("domínios do certificado: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("domínios do certificado: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("domínios do certificado: %w", err)
	}
	return out, nil
}

// DeleteCertificate apaga o certificado. Em uso → ErrCertInUse; inexistente → ErrNotFound.
func (d *DB) DeleteCertificate(ctx context.Context, id int64) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("apagar certificado: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var used int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM domains WHERE cert_id = ?`, id).Scan(&used); err != nil {
		return fmt.Errorf("apagar certificado: %w", err)
	}
	if used > 0 {
		return ErrCertInUse
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM certificates WHERE id = ?`, id)
	if isForeignKey(err) {
		// Um domínio passou a usar o certificado entre a checagem e o DELETE.
		return ErrCertInUse
	}
	if err != nil {
		return fmt.Errorf("apagar certificado: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("apagar certificado: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
