package store

import (
	"errors"
	"strings"
)

var (
	// ErrNotFound is returned when a requested row does not exist.
	ErrNotFound = errors.New("não encontrado")
	// ErrSetupDone is returned when the initial admin already exists.
	ErrSetupDone = errors.New("configuração inicial já concluída")
	// ErrConflict é devolvido quando uma restrição UNIQUE é violada.
	ErrConflict = errors.New("já existe")
)

// isUnique informa se err é uma violação de UNIQUE/PRIMARY KEY do SQLite.
func isUnique(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "UNIQUE constraint failed") ||
		strings.Contains(m, "PRIMARY KEY constraint failed")
}
