package deploy

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

var (
	slugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	repoRe    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	branchRe  = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,255}$`)
	serviceRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	imgHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)
	imgCompRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*$`)
	imgTagRe  = regexp.MustCompile(`^[\w][\w.-]{0,127}$`)
	imgDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// ValidSlug valida o slug de aplicação.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// ValidRepo valida "dono/nome" de um repositório GitHub.
func ValidRepo(s string) bool {
	if !repoRe.MatchString(s) {
		return false
	}
	owner, name, _ := strings.Cut(s, "/")
	return owner != "." && owner != ".." && name != "." && name != ".."
}

// ValidGitURL aceita só https://host/caminho, sem usuário/senha.
func ValidGitURL(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n\x00\\") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Opaque != "" {
		return false
	}
	return hostRe.MatchString(u.Hostname()) && !strings.Contains(s, "@")
}

// ValidBranch valida o nome de branch.
func ValidBranch(s string) bool {
	return branchRe.MatchString(s) && !strings.Contains(s, "..") &&
		!strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "/") &&
		!strings.HasSuffix(s, "/") && !strings.HasSuffix(s, ".lock")
}

// ValidRelPath valida caminho relativo, sem "..", dentro do clone.
func ValidRelPath(s string) bool {
	if s == "" || len(s) > 512 || strings.ContainsAny(s, "\x00\\:") ||
		strings.HasPrefix(s, "/") || strings.HasPrefix(s, "-") || path.IsAbs(s) {
		return false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// ValidServiceName valida o nome de serviço do Compose.
func ValidServiceName(s string) bool { return serviceRe.MatchString(s) }

// ValidEnvName valida o nome de variável de ambiente.
func ValidEnvName(s string) bool { return envNameRe.MatchString(s) }

// ValidPort valida porta 1-65535.
func ValidPort(p int) bool { return p >= 1 && p <= 65535 }

// ValidImageRef valida [registro[:porta]/]caminho[:tag][@sha256:hex].
func ValidImageRef(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	if i := strings.Index(s, "@"); i >= 0 {
		if !imgDigest.MatchString(s[i+1:]) {
			return false
		}
		s = s[:i]
	}
	parts := strings.Split(s, "/")
	last := parts[len(parts)-1]
	if i := strings.LastIndex(last, ":"); i >= 0 {
		if !imgTagRe.MatchString(last[i+1:]) {
			return false
		}
		parts[len(parts)-1] = last[:i]
	}
	if len(parts) > 1 {
		first := parts[0]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			if !imgHostRe.MatchString(first) {
				return false
			}
			parts = parts[1:]
		}
	}
	for _, c := range parts {
		if !imgCompRe.MatchString(c) {
			return false
		}
	}
	return true
}
