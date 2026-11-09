# Migrando um nginx existente para o Caddy do KoreGO

Este guia é para quem já tem um nginx no host ocupando as portas 80 e 443 e quer que o KoreGO passe a cuidar do proxy e do HTTPS. Enquanto o nginx segura as portas, o Caddy do KoreGO roda em **modo ensaio**, em portas de teste (`127.0.0.1:18080` e `127.0.0.1:18443`). Você cadastra e confere os sites com o nginx no ar e, quando estiver tudo certo, pede ao KoreGO para **assumir as portas**. Se algo falhar, ele volta sozinho para o nginx; depois da troca, o botão **Voltar para o nginx** desfaz tudo. O KoreGO só para o nginx quando você pede, e nunca lê nem altera os arquivos dele.

Vale também para o apache2 (`apache2.service`). Os exemplos usam nomes fictícios: `loja.exemplo.net` (uma SPA em `/var/www/loja/current` com a API em `127.0.0.1:4000`), `exemplo.com` (uma aplicação em `127.0.0.1:3000`), o painel em `painel.exemplo.com` e o servidor `203.0.113.10`.

## Antes de começar

- O KoreGO já está instalado e você consegue entrar no painel (`http://203.0.113.10:8000`).
- O nginx roda pelo systemd (`systemctl status nginx` mostra `active`). Um nginx iniciado à mão, ou outro programa nas portas, não é parado pelo KoreGO: veja "Outro processo nas portas" no fim.
- Em **Domínios**, o banner do proxy mostra "Ensaio: o Caddy roda em portas de teste enquanto nginx (pid …) ocupa 80/443".
- Para domínios com certificado **automático** (Let's Encrypt), o DNS precisa apontar para o servidor (ou para a Cloudflare) e as portas 80 e 443 precisam estar alcançáveis de fora: o certificado é emitido logo depois da troca, pelo desafio HTTP-01. Domínios com **certificado próprio** não dependem disso.
- A troca em si leva poucos segundos entre parar o nginx e o Caddy responder.

## 1. Backup

```sh
sudo tar czf /root/backup-nginx-$(date +%F).tar.gz /etc/nginx /etc/letsencrypt /etc/ssl
```

Guarde a cópia fora do servidor também.

## 2. Ler a configuração do nginx

```sh
sudo grep -R -n -e server_name -e proxy_pass -e root -e add_header -e client_max_body_size /etc/nginx/sites-enabled
```

Anote, para cada `server_name`:

| No nginx | No KoreGO |
| --- | --- |
| `location /api/ { proxy_pass http://127.0.0.1:4000; }` | rota `/api/` → **Endereço** `127.0.0.1:4000` |
| `location / { root /var/www/loja/current; try_files $uri /index.html; }` | rota `/` → **Pasta** `/var/www/loja/current` com **SPA** |
| `location /assets/ { expires max; }` | **Cache imutável** `/assets/` na pasta |
| `client_max_body_size 20m;` | **Limite do corpo** 20 MB na rota de endereço |
| `proxy_buffering off;` | **Sem buffer** na rota de endereço |
| `add_header Content-Security-Policy "...";` | cabeçalho na aba **Segurança** |
| `ssl_certificate` de origem da Cloudflare ou curinga | certificado **Próprio** |

Pastas fora de `/var/www` precisam ser movidas (ou ganhar um symlink) para dentro de `/var/www`: é a única pasta que o Caddy enxerga.

## 3. Cadastrar os sites

1. Certificados próprios primeiro: em **Domínios → Certificados → Adicionar certificado**, cole o PEM (cadeia + chave) ou informe o caminho do arquivo no servidor (por exemplo o certificado de origem da Cloudflare usado hoje pelo nginx). Um curinga `*.exemplo.net` é guardado uma vez e usado por vários domínios.
2. **Domínios → Adicionar domínio** para cada site:
   - **Rotas**: a rota `/` e as rotas por caminho, como na tabela acima.
   - **Certificado**: **Automático** ou **Próprio** (escolha o certificado guardado).
   - **Segurança**: **Só Cloudflare** se o site só deve ser acessado pela Cloudflare, os cabeçalhos que o nginx enviava e a **Compressão**.

Os domínios ficam salvos e passam a responder no ensaio na hora; o nginx continua atendendo o público.

## 4. Comparar no ensaio

O banner do proxy lista cada domínio com o resultado do ensaio: handshake TLS, nomes e validade do certificado, status de cada rota e, nas pastas, se o `index.html` saiu. Domínios com certificado automático aparecem como "certificado pendente até assumir as portas".

Compare também, pelo SSH, a resposta pública (nginx) com a do ensaio para alguns caminhos de cada site: a página inicial, um caminho da API, um arquivo de `/assets/` e um caminho desconhecido da SPA.

```sh
for p in / /api/saude /assets/app.js /rota/qualquer; do
  curl -s -o /tmp/pub -D - "https://loja.exemplo.net$p" | grep -v -i -e '^date:' -e '^server:' -e '^cf-' > /tmp/pub.h
  curl -sk -o /tmp/ens -D - --resolve loja.exemplo.net:18443:127.0.0.1 "https://loja.exemplo.net:18443$p" | grep -v -i -e '^date:' -e '^server:' > /tmp/ens.h
  echo "== $p"; diff /tmp/pub.h /tmp/ens.h; sha256sum /tmp/pub /tmp/ens
done
```

O status, os cabeçalhos (sem `Date`, `Server` e os que a Cloudflare acrescenta) e o hash do corpo devem bater. Diferenças esperadas: o KoreGO nunca envia `Server`, e um pedido para `/api` sem a barra final recebe `308` (o nginx costuma mandar `301`). Faça também um teste de uso da aplicação, como o login e uma chamada da API.

## 5. Assumir as portas

Num horário de pouco tráfego, clique em **Assumir as portas** no banner e confirme com o código do 2FA (ou um código de recuperação). O progresso aparece ao vivo, e a edição de domínios fica bloqueada até o fim. O KoreGO:

1. confere o ensaio de novo e recusa se algum domínio falhar (nada é alterado);
2. roda `systemctl stop nginx` e sobe o Caddy em 80/443, tentando por até 5 s enquanto o nginx solta as portas;
3. confere cada domínio em `127.0.0.1:443`; certificados automáticos têm até 60 s para sair;
4. se o passo 2 ou 3 falhar, volta sozinho: Caddy de novo no ensaio, `systemctl start nginx` e a mensagem "voltou para o nginx: <motivo>";
5. com tudo certo, roda `systemctl disable nginx` e o banner passa a mostrar "Migrado do nginx em <data>".

Depois, repita a comparação do passo 4 contra a resposta pública e confira no navegador.

## Voltar para o nginx

Enquanto o arquivo da unit existir, o banner mostra **Voltar para o nginx**. Confirmado com o código do 2FA, ele põe o Caddy de volta no ensaio (fora de 80/443) e roda `systemctl enable --now nginx`. Os arquivos em `/etc/nginx` e os certificados do certbot não foram alterados pela troca, então o nginx volta exatamente como estava. Os domínios continuam cadastrados no KoreGO, prontos para uma nova tentativa.

Apagar o nginx e o certbot depois que tudo estiver estável fica a seu critério; enquanto eles existirem, a volta continua disponível.

## Outro processo nas portas

Se as portas estão com um processo que não é o `nginx.service` ou o `apache2.service` do systemd, **Assumir as portas** recusa e diz qual processo é. Pare esse processo você mesmo; com as portas livres, o KoreGO sobe o Caddy em 80/443 na próxima verificação automática (até 30 segundos). Nesse caso não há o botão de voltar: para devolver as portas, pare o KoreGO e o Caddy antes de iniciar o outro servidor (`sudo systemctl stop korego && sudo docker stop korego-caddy`).

## Painel em HTTPS e porta 8000

Depois que o proxy estiver em 80/443, você pode servir o próprio painel em HTTPS.

1. Aponte um domínio para o servidor (por exemplo `painel.exemplo.com`, DNS apontando para `203.0.113.10`, ou um registro com proxy na Cloudflare).
2. No painel, abra **Configurações → Painel**, preencha **Domínio do painel** (`painel.exemplo.com`) e clique em **Salvar domínio**. Em **Domínios**, o botão **Editar** do domínio do painel abre as abas **Certificado** (por exemplo o curinga guardado) e **Segurança** (**Só Cloudflare**, cabeçalhos). Ligue o **Só Cloudflare** antes de desligar a porta 8000: com ela desligada, o KoreGO recusa ligar essa opção no painel.
3. Abra `https://painel.exemplo.com` e confirme que o login, as telas ao vivo (WebSocket) e a URL do webhook do GitHub funcionam.
4. Só então, ainda em **Configurações → Painel** e acessando por `https://painel.exemplo.com`, desligue a chave **Porta 8000 pública** e confirme em **Desligar porta 8000**. A chave fica desabilitada até o certificado do domínio estar ativo **e** você estar acessando o painel por `https://<domínio>`. Se tiver firewall, pode fechar a 8000 também.

### Recuperação se você perder o acesso

Se o domínio do painel parar de funcionar e a porta 8000 estiver desligada, reative-a pelo SSH:

```sh
sudo korego painel --porta-publica
sudo systemctl restart korego
```

Depois acesse `http://203.0.113.10:8000`, corrija o domínio e desligue a porta de novo.
