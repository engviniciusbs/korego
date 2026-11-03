#!/bin/sh
# Testa scripts/install.sh num container ubuntu:24.04 com Docker simulado.
# Uso: sh scripts/test-install.sh   (requer go e docker)
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# Caminhos para o docker (no Git Bash do Windows precisam ser C:/...).
if command -v cygpath >/dev/null 2>&1; then
	HOST_ROOT=$(cygpath -m "$ROOT")
	HOST_TMP=$(cygpath -m "$TMP")
else
	HOST_ROOT=$ROOT
	HOST_TMP=$TMP
fi
export MSYS_NO_PATHCONV=1

echo "==> compilando korego para linux/amd64"
(cd "$ROOT" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$HOST_TMP/korego" ./cmd/korego)

cat >"$TMP/inner.sh" <<'INNER'
set -u
fails=0
fail() { echo "FALHOU: $1"; fails=$((fails + 1)); }
ok() { echo "ok: $1"; }

# docker falso: responde apenas ao que o instalador pergunta.
mkdir -p /tmp/fakebin
cat >/tmp/fakebin/docker <<'FAKE'
#!/bin/sh
case "$*" in
	"compose version") echo "Docker Compose version v2.0.0-fake" ;;
	"--version") echo "Docker version 0.0.0-fake" ;;
	*) echo "docker falso: comando nao suportado: $*" >&2; exit 1 ;;
esac
FAKE
chmod +x /tmp/fakebin/docker
export PATH="/tmp/fakebin:$PATH"

export KOREGO_LOCAL_BINARY=/work/korego KOREGO_SKIP_SERVICE=1 KOREGO_YES=1

echo "==> primeira execucao"
if sh /repo/scripts/install.sh; then ok "install.sh terminou com sucesso"; else fail "install.sh retornou erro"; fi

[ -x /usr/local/bin/korego ] && ok "binario instalado e executavel" || fail "binario ausente ou nao executavel"
ver=$(/usr/local/bin/korego version 2>&1) && ok "korego version: $ver" || fail "korego version falhou: $ver"
[ "$(stat -c %a /var/lib/korego 2>/dev/null)" = 750 ] && ok "/var/lib/korego 0750" || fail "/var/lib/korego deveria ser 0750"
[ "$(stat -c %a /etc/korego 2>/dev/null)" = 700 ] && ok "/etc/korego 0700" || fail "/etc/korego deveria ser 0700"
[ -f /etc/korego/config.toml ] && ok "config.toml criado" || fail "config.toml ausente"
for want in 'listen = ":8000"' 'data_dir = "/var/lib/korego"' 'config_dir = "/etc/korego"'; do
	grep -qF "$want" /etc/korego/config.toml && ok "config.toml contem $want" || fail "config.toml sem $want"
done
[ -f /etc/systemd/system/korego.service ] && ok "korego.service instalado" || fail "korego.service ausente"

echo "==> segunda execucao (nao deve sobrescrever config.toml)"
echo '# editado pelo teste' >>/etc/korego/config.toml
before=$(cksum </etc/korego/config.toml)
if sh /repo/scripts/install.sh; then ok "segunda execucao terminou com sucesso"; else fail "segunda execucao retornou erro"; fi
after=$(cksum </etc/korego/config.toml)
[ "$before" = "$after" ] && ok "config.toml preservado" || fail "config.toml foi sobrescrito"

if [ "$fails" -ne 0 ]; then echo "$fails verificacao(oes) falharam"; exit 1; fi
echo "todas as verificacoes passaram"
INNER

echo "==> rodando install.sh em ubuntu:24.04"
docker run --rm \
	-v "$HOST_ROOT:/repo:ro" \
	-v "$HOST_TMP:/work" \
	ubuntu:24.04 sh /work/inner.sh
