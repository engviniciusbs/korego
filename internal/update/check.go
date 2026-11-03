// Package update implementa a autoatualização do KoreGO: consulta de releases,
// download com verificação de checksum e rollback.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/engviniciusbs/korego/internal/version"
)

const (
	defaultBaseURL = "https://api.github.com"
	defaultRepo    = "engviniciusbs/korego"
	checkTimeout   = 15 * time.Second
	maxAPIBody     = 1 << 20
)

// Release descreve a última versão publicada e os assets para a arquitetura pedida.
type Release struct {
	Version      string
	TarballURL   string
	ChecksumsURL string
}

// Checker consulta a API de releases do GitHub.
type Checker struct {
	BaseURL string
	Repo    string
	HTTP    *http.Client
}

// Latest devolve a última release com os assets de arch ("amd64" ou "arm64").
func (c *Checker) Latest(ctx context.Context, arch string) (Release, error) {
	base, repo := c.BaseURL, c.Repo
	if base == "" {
		base = defaultBaseURL
	}
	if repo == "" {
		repo = defaultRepo
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "korego/"+version.Version)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("consultar releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("consultar releases: status %d", resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBody)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("ler resposta de releases: %w", err)
	}
	if body.TagName == "" {
		return Release{}, fmt.Errorf("release sem tag")
	}
	tarName := fmt.Sprintf("korego_%s_linux_%s.tar.gz", strings.TrimPrefix(body.TagName, "v"), arch)
	rel := Release{Version: body.TagName}
	for _, a := range body.Assets {
		switch a.Name {
		case tarName:
			rel.TarballURL = a.URL
		case checksumsName:
			rel.ChecksumsURL = a.URL
		}
	}
	if rel.TarballURL == "" {
		return Release{}, fmt.Errorf("asset %s não encontrado na release %s", tarName, body.TagName)
	}
	if rel.ChecksumsURL == "" {
		return Release{}, fmt.Errorf("asset %s não encontrado na release %s", checksumsName, body.TagName)
	}
	return rel, nil
}

// Available informa se latest é mais novo que current. Builds "dev" nunca
// se atualizam. Aceita versões com ou sem o prefixo "v".
func Available(current, latest string) bool {
	if current == "dev" {
		return false
	}
	c, l := withV(current), withV(latest)
	if !semver.IsValid(c) || !semver.IsValid(l) {
		return false
	}
	return semver.Compare(l, c) > 0
}

func withV(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
