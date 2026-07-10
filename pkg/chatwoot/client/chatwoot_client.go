package chatwoot_client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	neturl "net/url"
	"time"
)

type Client struct {
	baseURL   string
	apiToken  string
	accountID string
	http      *http.Client
}

func NewClient(baseURL, apiToken, accountID string) *Client {
	return &Client{
		baseURL:   baseURL,
		apiToken:  apiToken,
		accountID: accountID,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

type Inbox struct {
	ID         int
	Identifier string
	Secret     string
}
type Contact struct{ ID int }
type Conversation struct{ ID int }

func (c *Client) do(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	url := fmt.Sprintf("%s/api/v1/accounts/%s%s", c.baseURL, c.accountID, path)
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", c.apiToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("chatwoot %s %s -> %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	if out != nil {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

// Ping valida a config chamando um endpoint leve da conta.
func (c *Client) Ping() error {
	return c.do(http.MethodGet, "/conversations", nil, nil)
}

func (c *Client) CreateInbox(name, webhookURL string) (*Inbox, error) {
	body := map[string]any{
		"name": name,
		"channel": map[string]any{
			"type":        "api",
			"webhook_url": webhookURL,
		},
	}
	// The inbox-create response returns id, inbox_identifier and secret at the top
	// level (secret is NOT nested under a "channel" object).
	var raw struct {
		ID         int    `json:"id"`
		Identifier string `json:"inbox_identifier"`
		Secret     string `json:"secret"`
	}
	if err := c.do(http.MethodPost, "/inboxes", body, &raw); err != nil {
		return nil, err
	}
	return &Inbox{ID: raw.ID, Identifier: raw.Identifier, Secret: raw.Secret}, nil
}

// FindOrCreateContact cria um contato e o contact_inbox com source_id.
// O Chatwoot deduplica por telefone; em caso de conflito, faz a busca por source_id.
func (c *Client) FindOrCreateContact(name, phone, sourceID string, inboxID int) (*Contact, error) {
	body := map[string]any{
		"name":         name,
		"phone_number": phone,
		"inbox_id":     inboxID,
		"source_id":    sourceID,
	}
	var raw struct {
		Payload struct {
			Contact struct {
				ID int `json:"id"`
			} `json:"contact"`
		} `json:"payload"`
	}
	if err := c.do(http.MethodPost, "/contacts", body, &raw); err != nil {
		return nil, err
	}
	return &Contact{ID: raw.Payload.Contact.ID}, nil
}

func (c *Client) CreateConversation(inboxID, contactID int, sourceID string) (*Conversation, error) {
	body := map[string]any{
		"inbox_id":   inboxID,
		"contact_id": contactID,
		"source_id":  sourceID,
	}
	var raw struct {
		ID int `json:"id"`
	}
	if err := c.do(http.MethodPost, "/conversations", body, &raw); err != nil {
		return nil, err
	}
	return &Conversation{ID: raw.ID}, nil
}

func (c *Client) CreateIncomingMessage(conversationID int, content, sourceID string) error {
	body := map[string]any{
		"content":      content,
		"message_type": "incoming",
		"source_id":    sourceID,
	}
	path := fmt.Sprintf("/conversations/%d/messages", conversationID)
	return c.do(http.MethodPost, path, body, nil)
}

// FindContactByPhone busca um contato existente pelo número de telefone.
// Retorna (nil, nil) quando nenhum contato é encontrado — isso não é um erro.
func (c *Client) FindContactByPhone(phone string) (*Contact, error) {
	body := map[string]any{
		"payload": []map[string]any{
			{
				"attribute_key":   "phone_number",
				"filter_operator": "equal_to",
				"values":          []string{phone},
			},
		},
	}
	var raw struct {
		Payload []struct {
			ID int `json:"id"`
		} `json:"payload"`
	}
	if err := c.do(http.MethodPost, "/contacts/filter", body, &raw); err != nil {
		return nil, err
	}
	if len(raw.Payload) == 0 {
		return nil, nil
	}
	return &Contact{ID: raw.Payload[0].ID}, nil
}

// FindOpenConversation retorna o display_id da primeira conversa com status
// "open" do contato, se houver. O display_id é o mesmo valor usado em
// /conversations/{id}/messages, retornado por CreateConversation.
func (c *Client) FindOpenConversation(contactID int) (int, bool, error) {
	path := fmt.Sprintf("/contacts/%d/conversations", contactID)
	var raw struct {
		Payload []struct {
			ID     int    `json:"id"`
			Status string `json:"status"`
		} `json:"payload"`
	}
	if err := c.do(http.MethodGet, path, nil, &raw); err != nil {
		return 0, false, err
	}
	for _, conv := range raw.Payload {
		if conv.Status == "open" {
			return conv.ID, true, nil
		}
	}
	return 0, false, nil
}

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

// maxDownloadBytes limita o tamanho de um download de anexo/mídia.
const maxDownloadBytes = 100 << 20 // 100 MiB

// DownloadBytes baixa uma URL como está (sem reescrever host), seguindo
// redirects normalmente. Use para URLs que já apontam para o host correto na
// rede interna (ex. presigned URL do MinIO) — reescrever o host quebraria a
// assinatura/servidor.
func (c *Client) DownloadBytes(rawURL string) ([]byte, string, error) {
	return c.download(rawURL, nil)
}

// DownloadFromChatwoot baixa uma URL do Chatwoot (ex. data_url de anexo, que
// usa o host externo FRONTEND_URL). Reescreve o scheme+host da URL inicial e
// de cada redirect para c.baseURL, mantendo a cadeia acessível na rede
// interna.
func (c *Client) DownloadFromChatwoot(rawURL string) ([]byte, string, error) {
	base, err := neturl.Parse(c.baseURL)
	if err != nil {
		return nil, "", err
	}
	rewrite := func(u *neturl.URL) *neturl.URL {
		u.Scheme = base.Scheme
		u.Host = base.Host
		return u
	}
	// O anexo pode ainda não estar disponível no storage no instante em que o
	// webhook dispara (race do upload direto do ActiveStorage, comum em notas de
	// voz). Tenta algumas vezes com backoff curto, dentro do timeout do webhook.
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(800 * time.Millisecond)
		}
		data, ct, err := c.download(rawURL, rewrite)
		if err == nil {
			return data, ct, nil
		}
		lastErr = err
	}
	return nil, "", lastErr
}

// download é o helper compartilhado por DownloadBytes e DownloadFromChatwoot.
// Se rewrite for não-nil, é aplicado à URL inicial e a cada hop de redirect.
func (c *Client) download(rawURL string, rewrite func(*neturl.URL) *neturl.URL) ([]byte, string, error) {
	target, err := neturl.Parse(rawURL)
	if err != nil {
		return nil, "", err
	}
	if rewrite != nil {
		target = rewrite(target)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if rewrite != nil {
				rewrite(req.URL)
			}
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxDownloadBytes {
		return nil, "", fmt.Errorf("attachment exceeds 100MiB")
	}
	return data, resp.Header.Get("Content-Type"), nil
}
