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

# apt-get falso: "instala" um git falso (o teste nao depende de rede).
cat >/tmp/fakebin/apt-get <<'FAKE'
#!/bin/sh
echo "apt-get $*" >>/tmp/apt.log
case "$*" in
	*"install"*git*)
		printf '#!/bin/sh\necho git version 0.0.0-fake\n' >/tmp/fakebin/git
		chmod +x /tmp/fakebin/git ;;
esac
exit 0
FAKE
chmod +x /tmp/fakebin/apt-get

# curl falso: grava o "script do Railpack" no arquivo pedido por -o.
cat >/tmp/fakebin/curl <<'FAKE'
#!/bin/sh
echo "curl $*" >>/tmp/curl.log
[ -z "${FAKE_CURL_FAIL:-}" ] || exit 22
out=
while [ $# -gt 0 ]; do
	[ "$1" = -o ] && out=$2
	shift
done
printf '#!/bin/sh\nprintf "#!/bin/sh\\necho railpack 0.0.0-fake\\n" >/tmp/fakebin/railpack\nchmod +x /tmp/fakebin/railpack\n' >"$out"
FAKE
chmod +x /tmp/fakebin/curl
export PATH="/tmp/fakebin:$PATH"

export KOREGO_LOCAL_BINARY=/work/korego KOREGO_SKIP_SERVICE=1 KOREGO_YES=1 KOREGO_RAILPACK=skip

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


echo "==> unit vem do script real, nunca do diretorio corrente"
rm -f /etc/systemd/system/korego.service
mkdir -p /bogus && echo 'ExecStart=/bin/evil' >/bogus/korego.service
if (cd /bogus && sh /repo/scripts/install.sh >/dev/null 2>&1); then ok "install.sh a partir de outro diretorio"; else fail "install.sh falhou a partir de outro diretorio"; fi
cmp -s /repo/scripts/korego.service /etc/systemd/system/korego.service && ok "unit instalado e o do repositorio" || fail "unit instalado difere do repositorio"
rm -f /etc/systemd/system/korego.service
if (cd /bogus && sh </repo/scripts/install.sh >/dev/null 2>&1); then fail "execucao via pipe nao deveria achar unit"; else ok "via pipe sem pacote: aborta"; fi
[ ! -e /etc/systemd/system/korego.service ] && ok "via pipe nao instalou unit do diretorio corrente" || fail "unit foi instalado via pipe"
echo "==> git ausente: instala pelo apt-get com KOREGO_YES=1"
rm -f /tmp/fakebin/git /tmp/apt.log
command -v git >/dev/null 2>&1 && fail "git deveria estar ausente antes do teste"
out=$(sh /repo/scripts/install.sh 2>&1) && ok "install.sh com git ausente terminou" || fail "install.sh com git ausente falhou: $out"
command -v git >/dev/null 2>&1 && ok "git instalado" || fail "git continua ausente"
grep -q 'install' /tmp/apt.log 2>/dev/null && ok "apt-get install chamado" || fail "apt-get install nao foi chamado"

echo "==> git presente: nao chama o apt-get"
rm -f /tmp/apt.log
sh /repo/scripts/install.sh >/dev/null 2>&1
[ ! -e /tmp/apt.log ] && ok "apt-get nao chamado com git presente" || fail "apt-get chamado sem necessidade"

echo "==> git ausente sem KOREGO_YES e sem terminal: aborta"
rm -f /tmp/fakebin/git
if (unset KOREGO_YES; sh /repo/scripts/install.sh </dev/null >/tmp/nogit.out 2>&1); then fail "deveria abortar sem git"; else grep -qi 'git' /tmp/nogit.out && ok "aborta mencionando o git" || fail "mensagem sem git"; fi
sh /repo/scripts/install.sh >/dev/null 2>&1

echo "==> KOREGO_RAILPACK=skip segue sem erro e informa"
rm -f /tmp/curl.log
out=$(KOREGO_RAILPACK=skip sh /repo/scripts/install.sh 2>&1) && ok "install.sh com railpack=skip terminou" || fail "railpack=skip falhou: $out"
printf '%s' "$out" | grep -qF 'Railpack não instalado; builds do tipo railpack vão falhar até instalá-lo.' && ok "mensagem de Railpack ausente" || fail "mensagem de Railpack ausente nao apareceu"
[ ! -e /tmp/curl.log ] && ok "skip nao baixou nada" || fail "skip baixou algo"

echo "==> KOREGO_RAILPACK=install baixa para arquivo e executa com sh"
rm -f /tmp/fakebin/railpack /tmp/curl.log
out=$(KOREGO_RAILPACK=install sh /repo/scripts/install.sh 2>&1) && ok "install.sh com railpack=install terminou" || fail "railpack=install falhou: $out"
command -v railpack >/dev/null 2>&1 && ok "railpack instalado" || fail "railpack ausente"
grep -q 'https://railpack.com/install.sh' /tmp/curl.log && grep -q -- '-o' /tmp/curl.log && ok "script baixado para arquivo (-o)" || fail "download do script do Railpack nao registrado"

echo "==> falha ao instalar o Railpack e apenas aviso"
rm -f /tmp/fakebin/railpack
out=$(FAKE_CURL_FAIL=1 KOREGO_RAILPACK=install sh /repo/scripts/install.sh 2>&1) && ok "falha do railpack nao e fatal" || fail "falha do railpack abortou: $out"
printf '%s' "$out" | grep -i 'aviso' | grep -qi 'railpack' && ok "aviso emitido" || fail "sem aviso"

if [ "$fails" -ne 0 ]; then echo "$fails verificacao(oes) falharam"; exit 1; fi
echo "todas as verificacoes passaram"
INNER

echo "==> rodando install.sh em ubuntu:24.04"
docker run --rm \
	-v "$HOST_ROOT:/repo:ro" \
	-v "$HOST_TMP:/work" \
	ubuntu:24.04 sh /work/inner.sh
