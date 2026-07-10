# Integração Chatwoot ↔ evolution-go — Fatia Mídia — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fazer mídia (imagem, áudio/voz, vídeo, documento) fluir nos dois sentidos entre WhatsApp e Chatwoot, estendendo o conector de texto existente.

**Architecture:** Entrada: o `chatwoot_producer` detecta mídia no envelope, resolve os bytes (base64 ou download de mediaUrl) e injeta no Chatwoot via upload multipart. Saída: o `webhook_handler` extrai os anexos do webhook, baixa da URL do Chatwoot (reescrevendo o host para a rede interna) e envia via `SendService.SendMediaFile`. Nenhum serviço novo.

**Tech Stack:** Go 1.25, Gin, whatsmeow. Testes `testing` + `httptest` (sem testify). Design: `docs/superpowers/specs/2026-07-10-integracao-chatwoot-midia-design.md`.

## Global Constraints

- **Suportar base64 E mediaUrl** no envelope de entrada (campo presente decide). MINIO_ENABLED=false (local) → base64; true (produção) → mediaUrl. Mesmo código.
- **Tipos:** imagem, áudio (voz/PTT com `is_voice_message`), vídeo, documento; sticker→imagem. Legenda (caption) → `content` da mensagem.
- Detecção de tipo pela sub-chave em `data.Message`: `imageMessage`/`audioMessage`/`videoMessage`/`documentMessage`/`stickerMessage`. Não há campo "type" unificado.
- **Entrada no Chatwoot:** `POST /conversations/{id}/messages` **multipart** com `attachments[]` (arquivo), `message_type=incoming`, `content`, `source_id`, `is_voice_message`.
- **Saída:** `data_url` do webhook usa host `FRONTEND_URL` (inacessível no container); reescrever host→`base_url` da config, seguindo redirects com reescrita de host em cada hop. Mapear `file_type`→`type`: image→image, video→video, audio→audio, file→document.
- Anti-eco (só outgoing, não-privado) e HMAC inalterados. Dedupe por wamid, cache de conversa e reconciliação pós-restart inalterados.
- Padrão de testes: package interno, `testing` stdlib + `httptest`, sem testify. Rodar go test dentro de Docker com deps cgo: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "apk add --no-cache git build-base libjpeg-turbo-dev libwebp-dev >/dev/null && go test ./pkg/chatwoot/... ./pkg/events/chatwoot/... 2>&1 | tail -30"`. Build: `docker build -t evolution-go:local .` (exit 0). `gofmt -w` os arquivos alterados.

---

### Task 1: Cliente — upload multipart de anexo + download de bytes

**Files:**
- Modify: `pkg/chatwoot/client/chatwoot_client.go`
- Modify: `pkg/chatwoot/client/chatwoot_client_test.go`

**Interfaces:**
- Produces:
  - `(*Client) CreateIncomingAttachment(conversationID int, content, sourceID string, fileBytes []byte, filename, contentType string, isVoice bool) error` — POST multipart para `/conversations/{id}/messages`.
  - `(*Client) DownloadBytes(url string) ([]byte, string, error)` — GET cru; retorna (bytes, content-type, erro). Segue redirects reescrevendo o host para `c.baseURL` em cada hop.

- [ ] **Step 1: Teste do multipart (falhando)** — em `chatwoot_client_test.go`, adicionar:

```go
func TestCreateIncomingAttachmentSendsMultipart(t *testing.T) {
	var gotType, gotContent, gotSource, gotVoice, gotFilename string
	var gotFileLen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("not multipart: %v", err)
		}
		gotType = r.FormValue("message_type")
		gotContent = r.FormValue("content")
		gotSource = r.FormValue("source_id")
		gotVoice = r.FormValue("is_voice_message")
		f, hdr, err := r.FormFile("attachments[]")
		if err != nil {
			t.Fatalf("no attachments[] file: %v", err)
		}
		defer f.Close()
		gotFilename = hdr.Filename
		b, _ := io.ReadAll(f)
		gotFileLen = len(b)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	err := c.CreateIncomingAttachment(7, "legenda", "wamid.X", []byte("PNGDATA"), "foto.jpg", "image/jpeg", true)
	if err != nil {
		t.Fatalf("CreateIncomingAttachment: %v", err)
	}
	if gotType != "incoming" || gotContent != "legenda" || gotSource != "wamid.X" || gotVoice != "true" || gotFilename != "foto.jpg" || gotFileLen != 7 {
		t.Fatalf("bad multipart: type=%q content=%q source=%q voice=%q file=%q len=%d", gotType, gotContent, gotSource, gotVoice, gotFilename, gotFileLen)
	}
}
```
Garantir os imports `io`, `mime/multipart`, `net/http`, `net/http/httptest` no teste.

- [ ] **Step 2: Rodar e ver falhar** — `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "apk add --no-cache git build-base libjpeg-turbo-dev libwebp-dev >/dev/null && go test ./pkg/chatwoot/client/... -run TestCreateIncomingAttachment -v"`. Esperado: FAIL (método não existe).

- [ ] **Step 3: Implementar os dois métodos** — em `chatwoot_client.go` (usar os imports `bytes`, `mime/multipart`, `fmt`, `io`, `net/http`, `net/url`):

```go
// CreateIncomingAttachment injeta uma mensagem incoming com um anexo via multipart.
func (c *Client) CreateIncomingAttachment(conversationID int, content, sourceID string, fileBytes []byte, filename, contentType string, isVoice bool) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("message_type", "incoming")
	if content != "" {
		_ = mw.WriteField("content", content)
	}
	if sourceID != "" {
		_ = mw.WriteField("source_id", sourceID)
	}
	if isVoice {
		_ = mw.WriteField("is_voice_message", "true")
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="attachments[]"; filename="%s"`, filename))
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	part, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := part.Write(fileBytes); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages", c.baseURL, c.accountID, conversationID)
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("api_access_token", c.apiToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("chatwoot attachment -> %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// DownloadBytes baixa uma URL. Se a URL apontar para outro host (ex. FRONTEND_URL
// do Chatwoot), reescreve o host para c.baseURL, e faz o mesmo em cada redirect,
// mantendo a cadeia acessível na rede interna.
func (c *Client) DownloadBytes(rawURL string) ([]byte, string, error) {
	base, err := neturl.Parse(c.baseURL)
	if err != nil {
		return nil, "", err
	}
	rewrite := func(u *neturl.URL) *neturl.URL {
		u.Scheme = base.Scheme
		u.Host = base.Host
		return u
	}
	target, err := neturl.Parse(rawURL)
	if err != nil {
		return nil, "", err
	}
	target = rewrite(target)

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			rewrite(req.URL)
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("api_access_token", c.apiToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download %s -> %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}
```
Adicionar os imports faltantes: `mime/multipart`, `net/textproto` (para `textproto.MIMEHeader`), `net/url` (aliased `neturl` para não colidir com nada), `time`. Manter o `do` existente intacto.

- [ ] **Step 4: Teste de download com host-rewrite (falhando→passando)** — adicionar:

```go
func TestDownloadBytesRewritesHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("BYTES"))
	}))
	defer srv.Close()
	// baseURL = srv; a URL de entrada usa outro host (localhost:9) que NÃO existe —
	// prova que o host foi reescrito para o do srv.
	c := NewClient(srv.URL, "tok", "1")
	data, ct, err := c.DownloadBytes("http://localhost:9/rails/active_storage/x/file.png")
	if err != nil {
		t.Fatalf("DownloadBytes: %v", err)
	}
	if string(data) != "BYTES" || ct != "image/png" {
		t.Fatalf("bad download: %q %q", data, ct)
	}
}
```

- [ ] **Step 5: Rodar todos os testes do cliente e ver passar** — comando do Global Constraints em `./pkg/chatwoot/client/...`. Esperado: PASS.

- [ ] **Step 6: gofmt + build + commit**

```bash
docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "gofmt -w pkg/chatwoot/client/"
docker build -t evolution-go:local .
git add pkg/chatwoot/client
git commit -m "feat(chatwoot): add multipart attachment upload and host-rewriting download"
```

---

### Task 2: Producer — detectar e injetar mídia entrante

**Files:**
- Modify: `pkg/events/chatwoot/chatwoot_producer.go`
- Modify: `pkg/events/chatwoot/chatwoot_producer_test.go`

**Interfaces:**
- Consumes: `(*Client) CreateIncomingAttachment`, `(*Client) DownloadBytes` (Task 1); `CreateIncomingMessage` (existente).
- Produces: `parseIncoming(payload []byte) (*incomingMsg, bool)` onde `incomingMsg` ganha `Media *mediaInfo` com `mediaInfo{ Base64, MediaURL, Mimetype, Filename string; IsVoice bool }`. `Text` continua sendo a legenda/corpo. `parseIncoming` NÃO faz IO (não decodifica base64 nem baixa URL).

- [ ] **Step 1: Testes de parse de mídia (falhando)** — em `chatwoot_producer_test.go`, adicionar um helper de envelope com mídia e casos:

```go
func mediaEnvelope(subkey, extraMsgFields string) []byte {
	return []byte(`{
		"event":"Message","instanceId":"inst-1",
		"data":{
			"Info":{"Sender":"5511988880001@s.whatsapp.net","Chat":"5511988880001@s.whatsapp.net","PushName":"Joao","ID":"wamid.M","IsFromMe":false},
			"Message":{"` + subkey + `":{"caption":"olha isso"}, ` + extraMsgFields + `}
		}
	}`)
}

func TestParseIncoming_ImageBase64(t *testing.T) {
	env := mediaEnvelope("imageMessage", `"base64":"QUJD","mimetype":"image/jpeg"`)
	m, ok := parseIncoming(env)
	if !ok || m.Media == nil {
		t.Fatal("esperado media")
	}
	if m.Media.Base64 != "QUJD" || m.Media.Mimetype != "image/jpeg" || m.Text != "olha isso" || m.Media.IsVoice {
		t.Fatalf("bad: %+v", m.Media)
	}
}

func TestParseIncoming_AudioIsVoice(t *testing.T) {
	env := mediaEnvelope("audioMessage", `"base64":"QUJD"`)
	m, ok := parseIncoming(env)
	if !ok || m.Media == nil || !m.Media.IsVoice {
		t.Fatalf("audio deveria marcar IsVoice: %+v", m)
	}
}

func TestParseIncoming_DocumentMediaUrl(t *testing.T) {
	env := mediaEnvelope("documentMessage", `"mediaUrl":"http://minio/x.pdf","mimetype":"application/pdf"`)
	m, ok := parseIncoming(env)
	if !ok || m.Media == nil || m.Media.MediaURL != "http://minio/x.pdf" || m.Media.Mimetype != "application/pdf" {
		t.Fatalf("bad doc: %+v", m)
	}
}

func TestParseIncoming_TextStillWorks(t *testing.T) {
	m, ok := parseIncoming(envelope("5511988880001@s.whatsapp.net", "5511988880001@s.whatsapp.net", "Joao", "ola", "wamid.A", false))
	if !ok || m.Media != nil || m.Text != "ola" {
		t.Fatalf("texto puro quebrou: %+v", m)
	}
}
```
(O helper `envelope(...)` já existe no arquivo de teste da fatia de texto.)

- [ ] **Step 2: Rodar e ver falhar** — `go test ./pkg/events/chatwoot/... -run TestParseIncoming -v` (via Docker com deps). Esperado: FAIL (parseIncoming não existe).

- [ ] **Step 3: Implementar parseIncoming + mediaInfo** — renomear/estender. Manter `parseIncomingText` NÃO é necessário; substituir seu uso por `parseIncoming` (que devolve texto e/ou mídia). Estrutura:

```go
type mediaInfo struct {
	Base64   string
	MediaURL string
	Mimetype string
	Filename string
	IsVoice  bool
}

// parseIncoming extrai texto e/ou mídia 1:1 do envelope. Puro (sem IO).
func parseIncoming(payload []byte) (*incomingMsg, bool) {
	var env struct {
		Event      string `json:"event"`
		InstanceID string `json:"instanceId"`
		Data       struct {
			Info struct {
				Sender   string `json:"Sender"`
				Chat     string `json:"Chat"`
				PushName string `json:"PushName"`
				ID       string `json:"ID"`
				IsFromMe bool   `json:"IsFromMe"`
			} `json:"Info"`
			Message json.RawMessage `json:"Message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, false
	}
	if env.Event != "Message" || env.Data.Info.IsFromMe {
		return nil, false
	}
	chat := env.Data.Info.Chat
	if strings.HasSuffix(chat, "@g.us") || strings.Contains(chat, "status@broadcast") {
		return nil, false
	}

	var msgMap map[string]json.RawMessage
	_ = json.Unmarshal(env.Data.Message, &msgMap)

	base := &incomingMsg{
		JID:        env.Data.Info.Sender,
		PushName:   env.Data.Info.PushName,
		Wamid:      env.Data.Info.ID,
		InstanceID: env.InstanceID,
	}

	// Campos irmãos adicionados pelo evolution ao nível de Message.
	var top struct {
		Base64   string `json:"base64"`
		MediaURL string `json:"mediaUrl"`
		Mimetype string `json:"mimetype"`
		Conversation    string `json:"conversation"`
		ExtendedTextMsg struct {
			Text string `json:"text"`
		} `json:"extendedTextMessage"`
	}
	_ = json.Unmarshal(env.Data.Message, &top)

	// Detecta mídia pela sub-chave presente.
	type mediaKind struct {
		key     string
		mime    string
		ext     string
		isVoice bool
	}
	kinds := []mediaKind{
		{"imageMessage", "image/jpeg", "jpg", false},
		{"videoMessage", "video/mp4", "mp4", false},
		{"audioMessage", "audio/ogg", "ogg", true},
		{"documentMessage", "application/octet-stream", "bin", false},
		{"stickerMessage", "image/png", "png", false},
	}
	for _, k := range kinds {
		sub, ok := msgMap[k.key]
		if !ok {
			continue
		}
		// caption e (para doc) filename/mimetype ficam dentro do sub-objeto.
		var subFields struct {
			Caption  string `json:"caption"`
			FileName string `json:"fileName"`
			Mimetype string `json:"mimetype"`
		}
		_ = json.Unmarshal(sub, &subFields)

		mime := top.Mimetype
		if mime == "" {
			mime = subFields.Mimetype
		}
		if mime == "" {
			mime = k.mime
		}
		filename := subFields.FileName
		if filename == "" {
			filename = env.Data.Info.ID + "." + k.ext
		}
		base.Text = subFields.Caption
		base.Media = &mediaInfo{
			Base64:   top.Base64,
			MediaURL: top.MediaURL,
			Mimetype: mime,
			Filename: filename,
			IsVoice:  k.isVoice,
		}
		// Sem bytes disponíveis (nem base64 nem mediaUrl) → não dá para injetar.
		if base.Media.Base64 == "" && base.Media.MediaURL == "" {
			return nil, false
		}
		return base, true
	}

	// Sem mídia: texto puro.
	text := top.Conversation
	if text == "" {
		text = top.ExtendedTextMsg.Text
	}
	if text == "" {
		return nil, false
	}
	base.Text = text
	return base, true
}
```
Adicionar `Media *mediaInfo` ao struct `incomingMsg`. Adicionar import `encoding/json` (já deve existir).

- [ ] **Step 4: Injetar mídia no handle()** — extrair a injeção num helper e usá-lo nos dois pontos (cache-hit e pós-reconcile). Substituir cada `client.CreateIncomingMessage(convID, msg.Text, msg.Wamid)` por `p.inject(client, convID, msg, log, userID)`:

```go
func (p *chatwootProducer) inject(client *chatwoot_client.Client, convID int, msg *incomingMsg, log logDest, userID string) {
	if msg.Media == nil {
		if err := client.CreateIncomingMessage(convID, msg.Text, msg.Wamid); err != nil {
			log.LogError("[%s] chatwoot: falha ao injetar mensagem: %v", userID, err)
		}
		return
	}
	fileBytes, err := resolveMediaBytes(client, msg.Media)
	if err != nil {
		log.LogError("[%s] chatwoot: falha ao obter bytes da mídia: %v", userID, err)
		return
	}
	if err := client.CreateIncomingAttachment(convID, msg.Text, msg.Wamid, fileBytes, msg.Media.Filename, msg.Media.Mimetype, msg.Media.IsVoice); err != nil {
		log.LogError("[%s] chatwoot: falha ao injetar anexo: %v", userID, err)
	}
}

// resolveMediaBytes decodifica o base64 ou baixa da mediaUrl (o que existir).
func resolveMediaBytes(client *chatwoot_client.Client, m *mediaInfo) ([]byte, error) {
	if m.Base64 != "" {
		return base64.StdEncoding.DecodeString(m.Base64)
	}
	data, _, err := client.DownloadBytes(m.MediaURL)
	return data, err
}
```
Trocar todas as chamadas de `parseIncomingText` por `parseIncoming` no `handle()`/`Produce()`, e **atualizar os testes existentes** que chamam `parseIncomingText` (os `TestParseIncomingText_*` da fatia de texto) para `parseIncoming` — ou remover os que ficaram redundantes com os novos `TestParseIncoming_*` (manter ao menos a cobertura de FromMe/grupo/não-Message). Sem isso o pacote não compila. Para o tipo do `log`: usar o tipo real que `loggerWrapper.GetLogger(userID)` retorna (verificar em `pkg/logger`); se for concreto, usar ele diretamente na assinatura do `inject` (não inventar uma interface `logDest`). Import `encoding/base64`.

- [ ] **Step 5: Rodar testes do producer e ver passar** — `go test ./pkg/events/chatwoot/... -v` (via Docker). Esperado: PASS (os 4 novos + os de texto existentes).

- [ ] **Step 6: gofmt + build + commit**

```bash
docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "gofmt -w pkg/events/chatwoot/"
docker build -t evolution-go:local .
git add pkg/events/chatwoot
git commit -m "feat(chatwoot): inject incoming whatsapp media into chatwoot"
```

---

### Task 3: Webhook receiver — enviar mídia do Chatwoot para o WhatsApp

**Files:**
- Modify: `pkg/chatwoot/handler/webhook_handler.go`
- Modify: `pkg/chatwoot/handler/webhook_handler_test.go`
- Modify: `pkg/chatwoot/handler/admin_handler.go` (não) — o webhook handler é construído em main.go; ver Task 4 para a nova dependência.

**Interfaces:**
- Consumes: `chatwoot_repository.ChatwootConfigRepository` (para `base_url`); `chatwoot_client.NewClient(...).DownloadBytes` (Task 1); `send_service.SendService.SendMediaFile` + `send_service.MediaStruct`.
- Produces: `shouldForward` passa a retornar também `[]outAttachment{ DataURL, FileType, ContentType, Filename string }`; helpers puros `fileTypeToMediaType(fileType string) string` e a extração de anexos.

- [ ] **Step 1: Testes das funções puras (falhando)** — em `webhook_handler_test.go`, adicionar:

```go
func TestShouldForward_WithAttachment(t *testing.T) {
	body := []byte(`{
		"event":"message_created","message_type":"outgoing","private":false,"content":"veja",
		"conversation":{"contact_inbox":{"source_id":"5511988880001@s.whatsapp.net"}},
		"attachments":[{"data_url":"http://localhost:3100/rails/x/foto.png","file_type":"image","extension":"png","content_type":"image/png"}]
	}`)
	jid, text, atts, ok := shouldForward(body)
	if !ok || jid != "5511988880001@s.whatsapp.net" || text != "veja" || len(atts) != 1 {
		t.Fatalf("bad: jid=%q text=%q atts=%d ok=%v", jid, text, len(atts), ok)
	}
	if atts[0].DataURL == "" || atts[0].FileType != "image" {
		t.Fatalf("bad att: %+v", atts[0])
	}
}

func TestFileTypeToMediaType(t *testing.T) {
	cases := map[string]string{"image": "image", "video": "video", "audio": "audio", "file": "document"}
	for in, want := range cases {
		if got := fileTypeToMediaType(in); got != want {
			t.Fatalf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestShouldForward_TextOnlyStillWorks(t *testing.T) {
	body := []byte(`{"event":"message_created","message_type":"outgoing","private":false,"content":"oi","conversation":{"contact_inbox":{"source_id":"x"}}}`)
	_, text, atts, ok := shouldForward(body)
	if !ok || text != "oi" || len(atts) != 0 {
		t.Fatalf("texto puro quebrou: text=%q atts=%d ok=%v", text, len(atts), ok)
	}
}
```
Ajustar os testes existentes de `shouldForward` para a nova assinatura de 4 retornos (adicionar `_` para o slice de anexos).

- [ ] **Step 2: Rodar e ver falhar** — `go test ./pkg/chatwoot/handler/... -v` (via Docker). Esperado: FAIL (assinatura/símbolos).

- [ ] **Step 3: Implementar** — em `webhook_handler.go`:

Adicionar tipo e ajustar `shouldForward`:
```go
type outAttachment struct {
	DataURL     string `json:"data_url"`
	FileType    string `json:"file_type"`
	ContentType string `json:"content_type"`
	Extension   string `json:"extension"`
}

func fileTypeToMediaType(fileType string) string {
	switch fileType {
	case "image":
		return "image"
	case "video":
		return "video"
	case "audio":
		return "audio"
	default:
		return "document"
	}
}

// shouldForward agora também devolve os anexos.
func shouldForward(body []byte) (jid, text string, attachments []outAttachment, ok bool) {
	var p struct {
		Event        string          `json:"event"`
		MessageType  string          `json:"message_type"`
		Private      bool            `json:"private"`
		Content      string          `json:"content"`
		Attachments  []outAttachment `json:"attachments"`
		Conversation struct {
			ContactInbox struct {
				SourceID string `json:"source_id"`
			} `json:"contact_inbox"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", "", nil, false
	}
	if p.Event != "message_created" || p.MessageType != "outgoing" || p.Private {
		return "", "", nil, false
	}
	if p.Conversation.ContactInbox.SourceID == "" {
		return "", "", nil, false
	}
	// Precisa ter conteúdo OU anexo.
	if p.Content == "" && len(p.Attachments) == 0 {
		return "", "", nil, false
	}
	return p.Conversation.ContactInbox.SourceID, p.Content, p.Attachments, true
}
```

Estender o `Handle` para: adicionar `configRepo` ao struct e ao `NewWebhookHandler`; após validar HMAC e `shouldForward`, se houver anexos, baixar e enviar cada um via `SendMediaFile`; senão, o `SendText` atual. Trecho novo do `Handle` (depois de obter `jid`, `text`, `attachments`):
```go
	number := jid
	if i := strings.IndexByte(number, '@'); i >= 0 {
		number = number[:i]
	}

	if len(attachments) == 0 {
		if _, err := h.sendService.SendText(&send_service.TextStruct{Number: number, Text: text}, instance); err != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa send failed: %v", instance.Id, err)
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"status": "sent"})
		return
	}

	cfg, err := h.configRepo.Get()
	if err != nil || cfg == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "config do chatwoot ausente"})
		return
	}
	client := chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID)
	for i, att := range attachments {
		fileBytes, ct, derr := client.DownloadBytes(att.DataURL)
		if derr != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa: falha ao baixar anexo: %v", instance.Id, derr)
			continue
		}
		if ct == "" {
			ct = att.ContentType
		}
		caption := ""
		if i == 0 {
			caption = text // legenda vai no primeiro anexo
		}
		filename := att.DataURL[strings.LastIndexByte(att.DataURL, '/')+1:]
		media := &send_service.MediaStruct{
			Number:   number,
			Type:     fileTypeToMediaType(att.FileType),
			Caption:  caption,
			Filename: filename,
		}
		if _, serr := h.sendService.SendMediaFile(media, fileBytes, instance); serr != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa: falha ao enviar mídia: %v", instance.Id, serr)
		}
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "sent"})
	return
```
Adicionar imports `chatwoot_client`, `chatwoot_repository` e ajustar `NewWebhookHandler(instanceRepo, sendService, configRepo, loggerWrapper)`. Remover o bloco antigo de `SendText` que ficava no fim (agora está no ramo sem anexos).

- [ ] **Step 4: Rodar testes do handler e ver passar** — `go test ./pkg/chatwoot/handler/... -v` (via Docker). Esperado: PASS.

- [ ] **Step 5: gofmt + build** — `gofmt -w pkg/chatwoot/handler/` e `docker build`. O build vai FALHAR até a Task 4 atualizar o call-site de `NewWebhookHandler` em main.go — tudo bem: aplicar a Task 4 e então buildar. (Não commitar ainda se o build falha; ver Task 4.)

- [ ] **Step 6: Commit** (após Task 4 compilar) — `git add pkg/chatwoot/handler && git commit -m "feat(chatwoot): forward chatwoot attachments to whatsapp"` (pode ser junto com a Task 4).

---

### Task 4: Wire da nova dependência + verificação end-to-end

**Files:**
- Modify: `cmd/evolution-go/main.go`

**Interfaces:**
- Consumes: `chatwoot_handler.NewWebhookHandler(instanceRepo, sendService, configRepo, loggerWrapper)` (nova assinatura da Task 3).

- [ ] **Step 1: Atualizar o call-site em main.go** — localizar por conteúdo a linha `chatwootWebhook := chatwoot_handler.NewWebhookHandler(instanceRepository, sendMessageService, loggerWrapper)` e passar `chatwootConfigRepo` como 3º argumento: `chatwoot_handler.NewWebhookHandler(instanceRepository, sendMessageService, chatwootConfigRepo, loggerWrapper)`. (`chatwootConfigRepo` já existe no escopo, criado na fatia de texto.)

- [ ] **Step 2: Build** — `docker build -t evolution-go:local .`. Esperado: exit 0 (compila as Tasks 3+4 juntas).

- [ ] **Step 3: Recriar o container e verificar boot** — `docker compose -f docker-compose.local.yml up -d --force-recreate evolution-go && sleep 8 && docker compose -f docker-compose.local.yml logs evolution-go --since 15s | grep -aiE "license activated|panic|fatal"`. Esperado: boot limpo (com CONNECT_ON_STARTUP=true a ritoDesk reconecta sozinha).

- [ ] **Step 4: Commit** — `git add cmd/evolution-go/main.go pkg/chatwoot/handler && git commit -m "feat(chatwoot): wire config repo into webhook handler for media"`.

- [ ] **Step 5: Verificação end-to-end (manual, guiada)** — com a ritoDesk conectada:
  - **Entrada:** enviar do WhatsApp uma **imagem** com legenda, um **áudio de voz (PTT)**, um **documento (PDF)** → devem aparecer na conversa do Chatwoot como anexos (imagem visível, áudio como nota de voz, PDF baixável), com a legenda no corpo.
  - **Saída:** pela tela do Chatwoot, **anexar e enviar** uma imagem e um PDF → devem chegar no WhatsApp.
  - Conferir logs do receiver se algum anexo falhar (`chatwoot->wa: falha ao baixar/enviar`).

## Notas

- MINIO_ENABLED permanece false (base64) no local. Para produção, ligar MINIO_ENABLED (+ vars) faz o producer usar `mediaUrl`/`DownloadBytes` automaticamente — nenhuma mudança de código.
- Vídeo/documento grandes em base64 inflam o evento; é a limitação conhecida do modo base64 (ver spec). O caminho MinIO resolve.
