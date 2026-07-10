# Integração Chatwoot ↔ evolution-go — Fatia 2: Mídia

**Data:** 2026-07-10
**Status:** Aprovado (design)
**Depende de:** a fatia de texto (spec `2026-07-10-integracao-chatwoot-evolution-go-design.md`), já implementada e em produção local.
**Escopo:** mídia bidirecional em conversas 1:1 — **imagem, áudio (incluindo nota de voz/PTT), vídeo e documento** (sticker tratado como imagem), com legenda (caption). Grupos e status seguem fora de escopo.

## Objetivo

Fazer mídia fluir nos dois sentidos entre WhatsApp e Chatwoot, reaproveitando ao máximo o que já existe (o `chatwoot_producer` para entrada, o `webhook_handler` para saída, o `chatwoot_client` e o `SendService`). Sem novos serviços obrigatórios.

## Decisão central: transporte que suporta base64 E MinIO

O envelope de evento do evolution entrega a mídia recebida de **uma de duas formas**, conforme `MINIO_ENABLED`:
- `MINIO_ENABLED=false` (padrão local): `data.Message.base64` (StdEncoding, sem prefixo `data:`).
- `MINIO_ENABLED=true` (produção): `data.Message.mediaUrl` (URL presigned do MinIO) + `data.Message.mimetype`.

O conector **detecta qual campo veio e trata os dois**. Assim, local usa base64 (zero infra) e produção liga o MinIO por config, **sem mudar código**. Essa é a resposta ao ponto de escalabilidade: base64 não é ideal para produção pesada (infla ~33% e trafega no evento em memória, duplicado por producer), mas o mesmo conector migra para URLs presigned só com o toggle `MINIO_ENABLED`.

## Entrada (WhatsApp → Chatwoot)

Estende o `chatwoot_producer`:

1. `parseIncomingText` passa a `parseIncoming`, que além do texto detecta mídia pela **sub-chave presente** em `data.Message` (`imageMessage`/`audioMessage`/`videoMessage`/`documentMessage`/`stickerMessage`) e extrai, **sem fazer IO** (mantém a função pura e testável):
   - a fonte dos bytes: `base64` (string) OU `mediaUrl` (o que existir);
   - `mimetype` (do envelope quando MinIO on; senão inferido do tipo: image→image/jpeg, audio→audio/ogg, video→video/mp4, sticker→image/png, document→o mimetype do próprio documento se disponível);
   - `filename` (para documentos; senão um nome derivado do wamid + extensão);
   - `isVoice` = true para áudio/PTT (o WhatsApp entrega voz como audioMessage);
   - `caption` (vira o `content` da mensagem no Chatwoot).
2. `handle()` resolve os bytes: decodifica o base64 **ou** baixa de `mediaUrl` via um helper do cliente. Depois injeta:
   - com mídia → novo método multipart do cliente `CreateIncomingAttachment(...)`;
   - sem mídia → o `CreateIncomingMessage(...)` atual.
   O dedupe por `wamid`, o cache de conversa e a reconciliação pós-restart permanecem iguais.
3. Novo método do cliente: **POST multipart** para `/conversations/{id}/messages` com `message_type=incoming`, `content` (caption), `source_id` (wamid), `is_voice_message` (para voz) e `attachments[]` = o arquivo (bytes + filename + content_type).

## Saída (Chatwoot → WhatsApp)

Estende o `webhook_handler`:

1. `shouldForward` passa a devolver também os `attachments[]` do payload (`data_url`, `file_type`, `content_type`, extensão). Continua respeitando o anti-eco (só `outgoing`, não-privado) e o HMAC.
2. Para cada anexo, **reescreve o host** do `data_url` (que usa `FRONTEND_URL`, ex. `http://localhost:3100/...`, inacessível de dentro do container) para o `base_url` da config global (ex. `http://chatwoot-rails:3000`). Como o `data_url` do ActiveStorage é um **redirect** (302), o download usa um `http.Client` com `CheckRedirect` que **reescreve o host de cada hop** para o `base_url`, mantendo toda a cadeia na rede interna.
3. Mapeia `file_type`→`type` do evolution: `image`→`image`, `video`→`video`, `audio`→`audio`, `file`→`document`. Chama `sendService.SendMediaFile(&MediaStruct{Number, Type, Caption, Filename}, bytes, instance)` (em processo, como já faz com `SendText`). Se houver texto + anexo, o texto vira `caption` do anexo (ou é enviado como mensagem de texto separada se o provedor não suportar caption naquele tipo).

O `webhook_handler` ganha acesso à `ChatwootConfigRepository` (para obter o `base_url` da reescrita de host).

## Fora de escopo (mantido)

- Grupos e status/stories.
- Confirmação de entrega por mensagem (`PUT /messages/{id}` status).
- Fila persistente de reenvio.
- Transcodificação além do que o evolution já faz (áudio→opus, sticker→png).

## Arquivos afetados

**evolution-go (alterados):** `pkg/events/chatwoot/chatwoot_producer.go` (parse+handle mídia), `pkg/chatwoot/client/chatwoot_client.go` (multipart + download), `pkg/chatwoot/handler/webhook_handler.go` (attachments+host-rewrite+SendMediaFile), `pkg/chatwoot/handler/admin_handler.go`/`cmd/evolution-go/main.go` (nova dependência configRepo no webhook handler), testes correspondentes.
**Chatwoot:** nenhuma mudança de código.
