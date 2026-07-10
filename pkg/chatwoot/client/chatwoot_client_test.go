package chatwoot_client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateInboxParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api_access_token") != "tok" {
			t.Fatalf("missing api_access_token header")
		}
		if r.URL.Path != "/api/v1/accounts/1/inboxes" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id":               42,
			"inbox_identifier": "abc123",
			"channel": map[string]any{"secret": "s3cr3t"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	inbox, err := c.CreateInbox("vendas", "http://evolution-go:8080/chatwoot/webhook/vendas")
	if err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}
	if inbox.ID != 42 || inbox.Identifier != "abc123" || inbox.Secret != "s3cr3t" {
		t.Fatalf("bad inbox: %+v", inbox)
	}
}

func TestCreateIncomingMessageSendsCorrectBody(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"id": 5})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	if err := c.CreateIncomingMessage(7, "olá", "wamid.X"); err != nil {
		t.Fatalf("CreateIncomingMessage: %v", err)
	}
	if got["content"] != "olá" || got["message_type"] != "incoming" || got["source_id"] != "wamid.X" {
		t.Fatalf("bad body: %+v", got)
	}
}
