# Migrando um nginx existente para o Caddy do KoreGO

Este guia é para quem já tem um nginx no host ocupando as portas 80 e 443 (com certificados do certbot) e quer que o KoreGO passe a cuidar do proxy e do HTTPS. O KoreGO nunca mexe no nginx sozinho: a migração é um passo explícito, feito por você.

Os exemplos usam domínios e endereços fictícios: `exemplo.com`, `api.exemplo.com`, o servidor `203.0.113.10` e aplicações em `127.0.0.1:3000` e `127.0.0.1:4000`.

## Antes de começar

- O KoreGO já está instalado e você consegue entrar no painel (`http://203.0.113.10:8000`).
- O DNS de cada domínio já aponta para o servidor. O Caddy obtém os certificados pelo desafio **HTTP-01**, então o domínio precisa resolver para o IP do servidor e as portas **80 e 443** precisam estar alcançáveis de fora (firewall e provedor).
- Haverá uma interrupção curta (em geral menos de 1 minuto) entre parar o nginx e o Caddy assumir as portas.

## 1. Backup

```sh
sudo tar czf /root/backup-nginx-$(date +%F).tar.gz /etc/nginx /etc/letsencrypt
```

Guarde a cópia fora do servidor também.

## 2. Ler os `proxy_pass`

```sh
sudo grep -R proxy_pass /etc/nginx/sites-enabled
```

Exemplo de saída:

```
/etc/nginx/sites-enabled/exemplo:    proxy_pass http://127.0.0.1:3000;
/etc/nginx/sites-enabled/api:        proxy_pass http://127.0.0.1:4000;
```

Anote, para cada `server_name`, a porta local de destino (`exemplo.com` → `127.0.0.1:3000`, `api.exemplo.com` → `127.0.0.1:4000`).

## 3. Cadastrar cada domínio como endereço manual

No painel: **Domínios → Adicionar domínio**. Em **Tipo de destino**, escolha a aba **Endereço**. Preencha **Hostname** (`exemplo.com`) e **Endereço (host:porta)** (`127.0.0.1:3000`) e clique em **Salvar**. Repita para cada site (`api.exemplo.com` → `127.0.0.1:4000`).

Enquanto o nginx estiver ativo, o Caddy não consegue subir nas portas 80 e 443; o painel mostra um aviso no proxy. Isso é esperado neste ponto.

## 4. Parar e desativar o nginx

```sh
sudo systemctl stop nginx && sudo systemctl disable nginx
```

## 5. Tentar novamente no proxy

Volte ao painel e clique em **Tentar novamente** no aviso do proxy. O KoreGO sobe o Caddy em 80/443 e ele emite os certificados de cada domínio.

O proxy só sai de "bloqueado" para ativo depois que o nginx parou **e** você clicou em **Tentar novamente**, ou depois da próxima verificação automática (até 30 segundos). Enquanto o nginx estiver ativo, o aviso continua.

## 6. Conferir cada domínio por HTTPS

```sh
curl -I https://exemplo.com
curl -I https://api.exemplo.com
```

Confira também no navegador. Se um domínio falhar na emissão, veja se o DNS aponta para `203.0.113.10` e se as portas 80/443 estão abertas, e use **Tentar novamente**.

## Voltar atrás (rollback)

Se algo der errado, devolva as portas ao nginx:

```sh
sudo systemctl stop korego && sudo docker stop korego-caddy && sudo systemctl enable --now nginx && sudo systemctl start korego
```

Depois confira se o nginx subiu:

```sh
systemctl status nginx
```

Por que nessa ordem: com o KoreGO parado, a reconciliação dele não pode subir o Caddy de novo entre o `docker stop` e o nginx pegar as portas. Quando o KoreGO volta, o nginx já está em 80/443; o KoreGO mostra o proxy como bloqueado e não sobe o Caddy.

Seus arquivos em `/etc/nginx` e os certificados em `/etc/letsencrypt` não foram alterados pela migração, então o nginx volta exatamente como estava.

## Painel em HTTPS e porta 8000

Depois que o proxy estiver funcionando, você pode servir o próprio painel em HTTPS.

1. Aponte um domínio para o servidor (por exemplo `painel.exemplo.com`, DNS apontando para `203.0.113.10`).
2. No painel, abra **Configurações → Painel**, preencha **Domínio do painel** (`painel.exemplo.com`) e clique em **Salvar domínio**. O Caddy emite o certificado e passa a encaminhar para o KoreGO.
3. Abra `https://painel.exemplo.com` e confirme que o login funciona.
4. Só então, ainda em **Configurações → Painel** e acessando por `https://painel.exemplo.com`, desligue a chave **Porta 8000 pública** e confirme em **Desligar porta 8000**. A chave fica desabilitada até o certificado do domínio estar ativo **e** você estar acessando o painel por `https://<domínio>`. Se tiver firewall, pode fechar a 8000 também.

### Recuperação se você perder o acesso

Se o domínio do painel parar de funcionar e a porta 8000 estiver desligada, reative-a pelo SSH:

```sh
sudo korego painel --porta-publica
sudo systemctl restart korego
```

Depois acesse `http://203.0.113.10:8000`, corrija o domínio e desligue a porta de novo.
