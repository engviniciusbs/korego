package migrate

import (
	"context"
	"strings"
	"time"
)

// ServiceManager controla units do systemd.
type ServiceManager interface {
	Stop(ctx context.Context, unit string) error
	Start(ctx context.Context, unit string) error
	Enable(ctx context.Context, unit string) error
	Disable(ctx context.Context, unit string) error
	// Active diz se a unit está ativa (systemctl is-active).
	Active(ctx context.Context, unit string) (bool, error)
	// Exists diz se o arquivo da unit existe.
	Exists(ctx context.Context, unit string) bool
	// UnitForProcess mapeia o dono das portas ("nginx (pid N)") para a unit
	// suportada: nginx → nginx, apache2/apache → apache2; o resto, false.
	UnitForProcess(process string) (string, bool)
}

// systemctlTimeout limita cada chamada ao systemctl.
const systemctlTimeout = 15 * time.Second

// supportedUnits são as únicas units que o motor para e inicia.
var supportedUnits = map[string]struct{}{"nginx": {}, "apache2": {}}

// processUnits mapeia o nome do processo para a unit.
var processUnits = map[string]string{"nginx": "nginx", "apache2": "apache2", "apache": "apache2"}

// UnitForProcess extrai o nome de "nome (pid N)" e devolve a unit suportada.
func UnitForProcess(process string) (string, bool) {
	i := strings.LastIndex(process, " (pid ")
	if i <= 0 || !strings.HasSuffix(process, ")") {
		return "", false
	}
	unit, ok := processUnits[process[:i]]
	return unit, ok
}
