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
			"secret":           "s3cr3t",
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

func TestFindContactByPhoneReturnsMatch(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/accounts/1/contacts/filter" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{
			"payload": []map[string]any{{"id": 123}},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	contact, err := c.FindContactByPhone("+5511988880001")
	if err != nil {
		t.Fatalf("FindContactByPhone: %v", err)
	}
	if contact == nil || contact.ID != 123 {
		t.Fatalf("bad contact: %+v", contact)
	}

	payload, ok := got["payload"].([]any)
	if !ok || len(payload) != 1 {
		t.Fatalf("bad request payload: %+v", got)
	}
	filter := payload[0].(map[string]any)
	if filter["attribute_key"] != "phone_number" || filter["filter_operator"] != "equal_to" {
		t.Fatalf("bad filter: %+v", filter)
	}
}

func TestFindContactByPhoneReturnsNilWhenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"payload": []map[string]any{}})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	contact, err := c.FindContactByPhone("+5511988880001")
	if err != nil {
		t.Fatalf("FindContactByPhone: %v", err)
	}
	if contact != nil {
		t.Fatalf("expected nil contact, got: %+v", contact)
	}
}

func TestFindOpenConversationReturnsOpenOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/accounts/1/contacts/123/conversations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"payload": []map[string]any{
				{"id": 10, "status": "resolved"},
				{"id": 45, "status": "open"},
				{"id": 99, "status": "open"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	convID, ok, err := c.FindOpenConversation(123)
	if err != nil {
		t.Fatalf("FindOpenConversation: %v", err)
	}
	if !ok || convID != 45 {
		t.Fatalf("expected open conversation 45, got convID=%d ok=%v", convID, ok)
	}
}

func TestFindOpenConversationReturnsFalseWhenNoneOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"payload": []map[string]any{
				{"id": 10, "status": "resolved"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	convID, ok, err := c.FindOpenConversation(123)
	if err != nil {
		t.Fatalf("FindOpenConversation: %v", err)
	}
	if ok || convID != 0 {
		t.Fatalf("expected no open conversation, got convID=%d ok=%v", convID, ok)
	}
}
