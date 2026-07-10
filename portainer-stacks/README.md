# Portainer Stack — evolution-go (API WhatsApp)

API de WhatsApp em Go, integrada ao Chatwoot. Imagem `ghcr.io/silascampos90/evolution-go`
(buildada pelo workflow `.github/workflows/build.yml`, ARM64).

Ingress via **Traefik** em `evo.ritodesk.com.br`. Sobe também um **Postgres** próprio
(cria `evogo_auth`/`evogo_users` sozinho) e um **MinIO** para a mídia.

## Pré-requisitos (uma vez, no Swarm)

Redes overlay externas compartilhadas:

```bash
docker network create --driver overlay --attachable chatwoot-evo
# traefik-public: normalmente já existe (a rede do seu Traefik). Se não:
docker network create --driver overlay --attachable traefik-public
```

## Variáveis de ambiente (definir no Portainer, na stack)

| Variável | Descrição |
|---|---|
| `GLOBAL_API_KEY` | chave de auth da API do evolution (header `apikey`). Gere forte. |
| `EVO_DB_PASSWORD` | senha do Postgres do evolution |
| `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` | credenciais do MinIO |
| `MINIO_BUCKET` | bucket de mídia (default `evolution-media`) |
| `CHATWOOT_SELF_URL` | URL deste evolution alcançável pelo Chatwoot; default `http://evolution-go:8080` (rede interna). Gravada no `webhook_url` das inboxes. |
| `IMAGE_TAG` | tag da imagem (default `latest`) |
| `EVO_DOMAIN` | default `evo.ritodesk.com.br` |
| `TRAEFIK_ENTRYPOINT` / `TRAEFIK_CERTRESOLVER` | ajuste aos nomes do seu Traefik (defaults `websecure` / `le`) |

## Deploy

1. Garanta as redes (acima) e o build da imagem (push no ghcr via o workflow).
2. No Portainer: **Stacks → Add stack**, cole `evolution-go-stack.yml`, preencha as variáveis, deploy.
3. Configure a integração no painel do evolution (`https://evo.ritodesk.com.br/chatwoot-admin`):
   - apikey = `GLOBAL_API_KEY`
   - **Config Chatwoot**: URL = `http://chatwoot-rails:3000` (rede interna), token de admin do Chatwoot, account = `1`.
   - **Nova conexão** → parear via QR.

## Notas

- O patch de licença offline está na imagem; ativa com o `GLOBAL_API_KEY` sem contatar servidor externo.
- Mídia usa MinIO (`MINIO_ENABLED=true`): o conector baixa a URL presigned interna e reenvia ao Chatwoot.
- Webhook do Traefik do Portainer: configure o secret `PORTAINER_WEBHOOK_URL_EVOLUTION` no GitHub para auto-deploy no push.
