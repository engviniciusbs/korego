// Package dockertest oferece um docker.Client falso para testes.
package dockertest

import (
	"context"

	"github.com/engviniciusbs/korego/internal/node/docker"
)

// Fake implementa docker.Client delegando às funções informadas.
// Método sem função devolve docker.ErrUnavailable.
type Fake struct {
	PingFn    func(ctx context.Context) error
	ListFn    func(ctx context.Context) ([]docker.Container, error)
	InspectFn func(ctx context.Context, id string) (docker.Details, error)
	StartFn   func(ctx context.Context, id string) error
	StopFn    func(ctx context.Context, id string) error
	RestartFn func(ctx context.Context, id string) error
	RemoveFn  func(ctx context.Context, id string) error
	LogsFn    func(ctx context.Context, id string, o docker.LogOptions, fn func(docker.LogLine)) error
	StatsFn   func(ctx context.Context, id string, fn func(docker.Stats)) error
	EventsFn  func(ctx context.Context, fn func(docker.Event)) error
}

var _ docker.Client = (*Fake)(nil)

// Ping chama PingFn.
func (f *Fake) Ping(ctx context.Context) error {
	if f.PingFn == nil {
		return docker.ErrUnavailable
	}
	return f.PingFn(ctx)
}

// List chama ListFn.
func (f *Fake) List(ctx context.Context) ([]docker.Container, error) {
	if f.ListFn == nil {
		return nil, docker.ErrUnavailable
	}
	return f.ListFn(ctx)
}

// Inspect chama InspectFn.
func (f *Fake) Inspect(ctx context.Context, id string) (docker.Details, error) {
	if f.InspectFn == nil {
		return docker.Details{}, docker.ErrUnavailable
	}
	return f.InspectFn(ctx, id)
}

// Start chama StartFn.
func (f *Fake) Start(ctx context.Context, id string) error {
	if f.StartFn == nil {
		return docker.ErrUnavailable
	}
	return f.StartFn(ctx, id)
}

// Stop chama StopFn.
func (f *Fake) Stop(ctx context.Context, id string) error {
	if f.StopFn == nil {
		return docker.ErrUnavailable
	}
	return f.StopFn(ctx, id)
}

// Restart chama RestartFn.
func (f *Fake) Restart(ctx context.Context, id string) error {
	if f.RestartFn == nil {
		return docker.ErrUnavailable
	}
	return f.RestartFn(ctx, id)
}

// Remove chama RemoveFn.
func (f *Fake) Remove(ctx context.Context, id string) error {
	if f.RemoveFn == nil {
		return docker.ErrUnavailable
	}
	return f.RemoveFn(ctx, id)
}

// Logs chama LogsFn.
func (f *Fake) Logs(ctx context.Context, id string, o docker.LogOptions, fn func(docker.LogLine)) error {
	if f.LogsFn == nil {
		return docker.ErrUnavailable
	}
	return f.LogsFn(ctx, id, o, fn)
}

// Stats chama StatsFn.
func (f *Fake) Stats(ctx context.Context, id string, fn func(docker.Stats)) error {
	if f.StatsFn == nil {
		return docker.ErrUnavailable
	}
	return f.StatsFn(ctx, id, fn)
}

// Events chama EventsFn.
func (f *Fake) Events(ctx context.Context, fn func(docker.Event)) error {
	if f.EventsFn == nil {
		return docker.ErrUnavailable
	}
	return f.EventsFn(ctx, fn)
}
