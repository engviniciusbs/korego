// Package config carrega a configuração do KoreGO (arquivo TOML + variáveis de ambiente).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/BurntSushi/toml"
)

// Config reúne as opções de execução do servidor.
type Config struct {
	ListenAddr string `toml:"listen"`
	DataDir    string `toml:"data_dir"`
	ConfigDir  string `toml:"config_dir"`
	DevProxy   string `toml:"dev_proxy"`
	// PM2Path é o caminho do binário pm2 (vazio = procurar no PATH).
	PM2Path string `toml:"pm2_path"`
	// PM2Home é o PM2_HOME do usuário que roda o PM2 (vazio = $PM2_HOME do
	// ambiente, senão $HOME/.pm2 do usuário do KoreGO).
	PM2Home string `toml:"pm2_home"`
	// PublicIP é o IP público da máquina, usado na verificação de DNS dos
	// domínios (vazio = só os IPs das interfaces).
	PublicIP string `toml:"public_ip"`
	// MaxBuilds é o número máximo de deploys construindo ao mesmo tempo.
	MaxBuilds int `toml:"max_builds"`
}

// Load lê o arquivo TOML em path (inexistente não é erro) e aplica as
// variáveis de ambiente KOREGO_* por cima.
func Load(path string) (Config, error) {
	c := Config{
		ListenAddr: ":8000",
		DataDir:    "/var/lib/korego",
		ConfigDir:  "/etc/korego",
		MaxBuilds:  2,
	}
	if _, err := toml.DecodeFile(path, &c); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("lendo configuração %s: %w", path, err)
	}
	override(&c.ListenAddr, "KOREGO_LISTEN")
	override(&c.DataDir, "KOREGO_DATA_DIR")
	override(&c.ConfigDir, "KOREGO_CONFIG_DIR")
	override(&c.DevProxy, "KOREGO_DEV_PROXY")
	override(&c.PM2Path, "KOREGO_PM2_PATH")
	override(&c.PM2Home, "KOREGO_PM2_HOME")
	override(&c.PublicIP, "KOREGO_PUBLIC_IP")
	if v := os.Getenv("KOREGO_MAX_BUILDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("KOREGO_MAX_BUILDS inválido %q: %w", v, err)
		}
		c.MaxBuilds = n
	}
	return c, nil
}

func override(dst *string, env string) {
	if v := os.Getenv(env); v != "" {
		*dst = v
	}
}
