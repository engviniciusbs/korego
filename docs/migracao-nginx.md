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

No painel: **Domínios → Novo domínio → Endereço**. Informe o domínio (`exemplo.com`) e o endereço de destino (`127.0.0.1:3000`). Repita para cada site (`api.exemplo.com` → `127.0.0.1:4000`).

Enquanto o nginx estiver ativo, o Caddy não consegue subir nas portas 80 e 443; o painel mostra um aviso no proxy. Isso é esperado neste ponto.

## 4. Parar e desativar o nginx

```sh
sudo systemctl stop nginx && sudo systemctl disable nginx
```

## 5. Tentar novamente no proxy

Volte ao painel e clique em **Tentar novamente** no aviso do proxy. O KoreGO sobe o Caddy em 80/443 e ele emite os certificados de cada domínio.

## 6. Conferir cada domínio por HTTPS

```sh
curl -I https://exemplo.com
curl -I https://api.exemplo.com
```

Confira também no navegador. Se um domínio falhar na emissão, veja se o DNS aponta para `203.0.113.10` e se as portas 80/443 estão abertas, e use **Tentar novamente**.

## Voltar atrás (rollback)

Se algo der errado, devolva as portas ao nginx:

```sh
docker stop korego-caddy && sudo systemctl enable --now nginx
```

Seus arquivos em `/etc/nginx` e os certificados em `/etc/letsencrypt` não foram alterados pela migração, então o nginx volta exatamente como estava.

## Painel em HTTPS e porta 8000

Depois que o proxy estiver funcionando, você pode servir o próprio painel em HTTPS.

1. Aponte um domínio para o servidor (por exemplo `painel.exemplo.com`, DNS apontando para `203.0.113.10`).
2. No painel, configure esse domínio como o domínio do painel. O Caddy emite o certificado e passa a encaminhar para o KoreGO.
3. Confirme que `https://painel.exemplo.com` abre e que o login funciona.
4. Só então desligue a porta pública 8000 pela própria configuração do painel. Se tiver firewall, pode fechar a 8000 também.

### Recuperação se você perder o acesso

Se o domínio do painel parar de funcionar e a porta 8000 estiver desligada, reative-a pelo SSH:

```sh
sudo korego painel --porta-publica
sudo systemctl restart korego
```

Depois acesse `http://203.0.113.10:8000`, corrija o domínio e desligue a porta de novo.
