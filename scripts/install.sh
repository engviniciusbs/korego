#!/bin/sh
# Instalador do KoreGO para Ubuntu/Debian.
#
# Variaveis de ambiente:
#   KOREGO_VERSION        versao a instalar (padrao: latest)
#   KOREGO_YES=1          nao pergunta nada (instala o Docker se faltar)
#   KOREGO_LOCAL_BINARY   caminho de um binario local; pula o download (testes)
#   KOREGO_SKIP_SERVICE=1 nao habilita/inicia o servico systemd
set -eu

REPO="engviniciusbs/korego"
BIN_PATH="/usr/local/bin/korego"
DATA_DIR="/var/lib/korego"
CONFIG_DIR="/etc/korego"
CONFIG_FILE="$CONFIG_DIR/config.toml"
UNIT_PATH="/etc/systemd/system/korego.service"
PORT=8000

info() { printf '==> %s\n' "$*"; }
warn() { printf 'aviso: %s\n' "$*" >&2; }
die() { printf 'erro: %s\n' "$*" >&2; exit 1; }

script_dir() { cd "$(dirname "$0")" && pwd; }

# --- 1. root, sistema e arquitetura ---------------------------------------
[ "$(id -u)" -eq 0 ] || die "execute como root (ex.: sudo sh install.sh)."

[ -r /etc/os-release ] || die "nao foi possivel identificar o sistema (/etc/os-release ausente)."
# shellcheck disable=SC1091
. /etc/os-release
case " ${ID:-} ${ID_LIKE:-} " in
	*" ubuntu "* | *" debian "*) ;;
	*) die "sistema nao suportado (${PRETTY_NAME:-desconhecido}); use Ubuntu ou Debian." ;;
esac

case "$(uname -m)" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) die "arquitetura nao suportada: $(uname -m)." ;;
esac

# --- 2. Docker -------------------------------------------------------------
have_docker() {
	command -v docker >/dev/null 2>&1 && docker --version >/dev/null 2>&1 &&
		docker compose version >/dev/null 2>&1
}

if have_docker; then
	info "Docker e Docker Compose encontrados."
else
	info "Docker ou 'docker compose' nao encontrado."
	if [ "${KOREGO_YES:-}" = 1 ]; then
		answer=y
	elif [ -r /dev/tty ]; then
		printf 'Instalar o Docker agora via https://get.docker.com? [s/N] ' >/dev/tty
		read -r answer </dev/tty || answer=n
	else
		die "Docker ausente. Instale-o ou rode novamente com KOREGO_YES=1."
	fi
	case "$answer" in
		y | Y | s | S | sim | SIM) ;;
		*) die "Docker e necessario. Instalacao cancelada." ;;
	esac
	command -v curl >/dev/null 2>&1 || die "curl e necessario para instalar o Docker."
	curl -fsSL https://get.docker.com | sh
	have_docker || die "o Docker nao ficou disponivel apos a instalacao."
fi

# --- 3. portas -------------------------------------------------------------
port_listeners() {
	ss -H -ltnp "sport = :$1" 2>/dev/null || true
}

if command -v ss >/dev/null 2>&1; then
	for p in 80 443; do
		out=$(port_listeners "$p")
		if [ -n "$out" ]; then
			warn "a porta $p esta ocupada (nada sera alterado): $(printf '%s' "$out" | tr -s ' ' | head -n 1)"
		fi
	done
	out=$(port_listeners "$PORT")
	if [ -n "$out" ]; then
		# Reinstalacao/atualizacao: a porta pode estar com o proprio korego.
		case "$out" in
			*'"korego"'*) info "a porta $PORT esta em uso pelo korego (atualizacao)." ;;
			*) die "a porta $PORT esta ocupada: $(printf '%s' "$out" | tr -s ' ' | head -n 1). Libere-a e tente novamente." ;;
		esac
	fi
else
	warn "comando 'ss' (iproute2) ausente; verificacao de portas ignorada."
fi

# --- 4. binario ------------------------------------------------------------
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [ -n "${KOREGO_LOCAL_BINARY:-}" ]; then
	[ -f "$KOREGO_LOCAL_BINARY" ] || die "KOREGO_LOCAL_BINARY nao existe: $KOREGO_LOCAL_BINARY"
	info "usando binario local $KOREGO_LOCAL_BINARY"
	SRC_BIN=$KOREGO_LOCAL_BINARY
else
	command -v curl >/dev/null 2>&1 || die "curl e necessario para baixar o KoreGO."
	command -v sha256sum >/dev/null 2>&1 || die "sha256sum e necessario para conferir o download."
	VERSION=${KOREGO_VERSION:-latest}
	if [ "$VERSION" = latest ]; then
		final=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
			die "nao foi possivel descobrir a ultima versao."
		VERSION=${final##*/}
	fi
	VERSION=${VERSION#v}
	[ -n "$VERSION" ] || die "versao invalida."
	ARCHIVE="korego_${VERSION}_linux_${ARCH}.tar.gz"
	BASE="https://github.com/$REPO/releases/download/v$VERSION"

	info "baixando KoreGO $VERSION ($ARCH)"
	curl -fsSL -o "$TMP/$ARCHIVE" "$BASE/$ARCHIVE" || die "falha ao baixar $ARCHIVE."
	curl -fsSL -o "$TMP/checksums.txt" "$BASE/checksums.txt" || die "falha ao baixar checksums.txt."

	expected=$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | head -n 1 | cut -d ' ' -f 1)
	[ -n "$expected" ] || die "checksum de $ARCHIVE nao encontrado em checksums.txt."
	actual=$(sha256sum "$TMP/$ARCHIVE" | cut -d ' ' -f 1)
	[ "$expected" = "$actual" ] || die "SHA-256 nao confere para $ARCHIVE."
	info "SHA-256 conferido."

	tar -xzf "$TMP/$ARCHIVE" -C "$TMP" korego || die "falha ao extrair o binario."
	SRC_BIN=$TMP/korego
fi

install -m 0755 "$SRC_BIN" "$BIN_PATH"
info "binario instalado em $BIN_PATH"

# --- 5. diretorios e configuracao -----------------------------------------
install -d -m 0750 "$DATA_DIR"
install -d -m 0700 "$CONFIG_DIR"
chmod 0750 "$DATA_DIR"
chmod 0700 "$CONFIG_DIR"

if [ -e "$CONFIG_FILE" ]; then
	info "$CONFIG_FILE ja existe; mantido."
else
	umask 077
	cat >"$CONFIG_FILE" <<CFG
listen = ":$PORT"
data_dir = "$DATA_DIR"
config_dir = "$CONFIG_DIR"
CFG
	info "criado $CONFIG_FILE"
fi

# --- 6. servico systemd ----------------------------------------------------
UNIT_SRC="$(script_dir)/korego.service"
SERVICE_STARTED=0
if [ -f "$UNIT_SRC" ]; then
	install -m 0644 "$UNIT_SRC" "$UNIT_PATH"
else
	cat >"$UNIT_PATH" <<UNIT
[Unit]
Description=KoreGO - gerenciador de VPS
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=$BIN_PATH serve --config $CONFIG_FILE
Restart=on-failure
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
	chmod 0644 "$UNIT_PATH"
fi

if [ "${KOREGO_SKIP_SERVICE:-}" = 1 ]; then
	info "KOREGO_SKIP_SERVICE=1: servico nao habilitado."
else
	command -v systemctl >/dev/null 2>&1 || die "systemctl nao encontrado."
	systemctl daemon-reload
	systemctl enable --now korego
	SERVICE_STARTED=1
fi

# --- 7. resumo -------------------------------------------------------------
IP=$(hostname -I 2>/dev/null | cut -d ' ' -f 1 || true)
[ -n "$IP" ] || IP="<IP-do-servidor>"
TOKEN_FILE="$CONFIG_DIR/setup-token"

echo
info "KoreGO instalado."
echo "Acesse: http://$IP:$PORT"

if [ "$SERVICE_STARTED" -eq 1 ]; then
	i=0
	while [ ! -s "$TOKEN_FILE" ] && [ "$i" -lt 10 ]; do
		sleep 1
		i=$((i + 1))
	done
fi
if [ "$SERVICE_STARTED" -eq 1 ] && [ -s "$TOKEN_FILE" ]; then
	echo "Token de setup: $(cat "$TOKEN_FILE")"
else
	echo "Token de setup: sudo cat $TOKEN_FILE"
fi
