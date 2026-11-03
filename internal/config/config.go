// Package config carrega a configuração do KoreGO (arquivo TOML + variáveis de ambiente).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
)

// Config reúne as opções de execução do servidor.
type Config struct {
	ListenAddr string `toml:"listen"`
	DataDir    string `toml:"data_dir"`
	ConfigDir  string `toml:"config_dir"`
	DevProxy   string `toml:"dev_proxy"`
}

// Load lê o arquivo TOML em path (inexistente não é erro) e aplica as
// variáveis de ambiente KOREGO_* por cima.
func Load(path string) (Config, error) {
	c := Config{
		ListenAddr: ":8000",
		DataDir:    "/var/lib/korego",
		ConfigDir:  "/etc/korego",
	}
	if _, err := toml.DecodeFile(path, &c); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("lendo configuração %s: %w", path, err)
	}
	override(&c.ListenAddr, "KOREGO_LISTEN")
	override(&c.DataDir, "KOREGO_DATA_DIR")
	override(&c.ConfigDir, "KOREGO_CONFIG_DIR")
	override(&c.DevProxy, "KOREGO_DEV_PROXY")
	return c, nil
}

func override(dst *string, env string) {
	if v := os.Getenv(env); v != "" {
		*dst = v
	}
}
