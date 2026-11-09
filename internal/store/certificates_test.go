package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCertificates_CRUDAndInUse(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()

	na := time.Unix(time.Now().Add(90*24*time.Hour).Unix(), 0)
	id, err := db.CreateCertificate(ctx, Certificate{Name: "loja", Issuer: "CA Exemplo",
		Names: []string{"loja.exemplo.com", "*.exemplo.net"}, NotAfter: na, Fingerprint: "ab:cd", PEMEnc: "enc"})
	require.NoError(t, err)
	require.NotZero(t, id)

	_, err = db.CreateCertificate(ctx, Certificate{Name: "outro", Fingerprint: "ab:cd", PEMEnc: "x"})
	require.ErrorIs(t, err, ErrConflict)

	got, err := db.CertificateByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "loja", got.Name)
	require.Equal(t, []string{"loja.exemplo.com", "*.exemplo.net"}, got.Names)
	require.True(t, na.Equal(got.NotAfter))
	require.Equal(t, "enc", got.PEMEnc)
	require.False(t, got.CreatedAt.IsZero())

	_, err = db.CertificateByID(ctx, 999)
	require.ErrorIs(t, err, ErrNotFound)

	list, err := db.ListCertificates(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	d := &Domain{Hostname: "loja.exemplo.com", TargetType: "address", Address: "127.0.0.1:9000",
		WWWRedirect: "none", TLSMode: "custom", CertID: &id}
	require.NoError(t, db.InsertDomain(ctx, d))
	users, err := db.DomainsUsingCert(ctx, id)
	require.NoError(t, err)
	require.Equal(t, []string{"loja.exemplo.com"}, users)
	require.ErrorIs(t, db.DeleteCertificate(ctx, id), ErrCertInUse)

	d.TLSMode, d.CertID = "auto", nil
	require.NoError(t, db.UpdateDomain(ctx, d))
	require.NoError(t, db.DeleteCertificate(ctx, id))
	require.ErrorIs(t, db.DeleteCertificate(ctx, id), ErrNotFound)
}

func TestIsForeignKey(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	id, err := db.CreateCertificate(ctx, Certificate{Name: "c", Fingerprint: "AA", PEMEnc: "x"})
	require.NoError(t, err)
	d := &Domain{Hostname: "exemplo.com", TargetType: "address", Address: "127.0.0.1:5000",
		WWWRedirect: "none", TLSMode: "custom", CertID: &id}
	require.NoError(t, db.InsertDomain(ctx, d))
	// Apagar direto, sem a checagem de DeleteCertificate, dispara a FK crua.
	_, err = db.sql.ExecContext(ctx, `DELETE FROM certificates WHERE id = ?`, id)
	require.Error(t, err)
	require.True(t, isForeignKey(err), err.Error())
	require.False(t, isForeignKey(nil))
	require.False(t, isUnique(err))
}
