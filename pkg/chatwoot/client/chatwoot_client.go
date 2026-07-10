package chatwoot_client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	var raw struct {
		ID         int    `json:"id"`
		Identifier string `json:"inbox_identifier"`
		Channel    struct {
			Secret string `json:"secret"`
		} `json:"channel"`
	}
	if err := c.do(http.MethodPost, "/inboxes", body, &raw); err != nil {
		return nil, err
	}
	return &Inbox{ID: raw.ID, Identifier: raw.Identifier, Secret: raw.Channel.Secret}, nil
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
