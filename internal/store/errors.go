package store

import "errors"

var (
	// ErrNotFound is returned when a requested row does not exist.
	ErrNotFound = errors.New("não encontrado")
	// ErrSetupDone is returned when the initial admin already exists.
	ErrSetupDone = errors.New("configuração inicial já concluída")
)
