package store

import (
	"context"
	"fmt"
	"time"
)

// AdoptStatic grava, numa só transação, a adoção de um site estático: a app,
// o deploy "adopt" já running com a pasta da versão no ar, o deploy como
// atual da app e os domínios com as rotas trocadas. Nas rotas de domains,
// as do tipo "app" sem AppID passam a apontar para a app criada (domains
// não é alterado). Em sucesso preenche a (ID, CurrentDeployID) e dep (ID). Qualquer erro desfaz tudo; slug repetido
// → ErrConflict, domínio inexistente → ErrNotFound.
func (d *DB) AdoptStatic(ctx context.Context, a *App, dep *Deploy, domains []Domain) error {
	type prepared struct {
		m                Domain
		headers, tlsMode string
	}
	preps := make([]prepared, 0, len(domains))
	for _, dm := range domains {
		headers, tlsMode, err := dm.normalize()
		if err != nil {
			return fmt.Errorf("adotar site: %w", err)
		}
		dm.Routes = append([]DomainRoute(nil), dm.Routes...) // a entrada não muda
		preps = append(preps, prepared{dm, headers, tlsMode})
	}
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("adotar site: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	app := *a
	app.CurrentDeployID = nil
	if err := insertApp(ctx, tx, &app); err != nil {
		return err
	}
	m := *dep
	m.AppID = app.ID
	if err := insertDeploy(ctx, tx, &m); err != nil {
		return err
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE deploys SET release_dir = ?, started_at = ?, finished_at = CASE WHEN status IN
		   ('running', 'failed', 'superseded', 'cancelled') THEN ? ELSE NULL END WHERE id = ?`,
		m.ReleaseDir, now, now, m.ID); err != nil {
		return fmt.Errorf("adotar site: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE apps SET current_deploy_id = ? WHERE id = ?`, m.ID, app.ID); err != nil {
		return fmt.Errorf("adotar site: %w", err)
	}
	for _, p := range preps {
		for i := range p.m.Routes {
			if r := &p.m.Routes[i]; r.TargetType == "app" && r.AppID == nil {
				id := app.ID
				r.AppID = &id
			}
		}
		if err := updateDomainTx(ctx, tx, &p.m, p.headers, p.tlsMode, now); err != nil {
			return fmt.Errorf("adotar site: domínio %s: %w", p.m.Hostname, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("adotar site: %w", err)
	}
	cur := m.ID
	app.CurrentDeployID = &cur
	*a, *dep = app, m
	return nil
}
