package chatwoot_client

import (
	"encoding/json"
	"io"
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

func TestDownloadFromChatwootRewritesHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("BYTES"))
	}))
	defer srv.Close()
	// baseURL = srv; a URL de entrada usa outro host (localhost:9) que NÃO existe —
	// prova que o host foi reescrito para o do srv.
	c := NewClient(srv.URL, "tok", "1")
	data, ct, err := c.DownloadFromChatwoot("http://localhost:9/rails/active_storage/x/file.png")
	if err != nil {
		t.Fatalf("DownloadFromChatwoot: %v", err)
	}
	if string(data) != "BYTES" || ct != "image/png" {
		t.Fatalf("bad download: %q %q", data, ct)
	}
}

func TestDownloadBytesDoesNotRewriteHost(t *testing.T) {
	var hitB bool
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server A should not receive any request, got %s", r.URL.Path)
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitB = true
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("MINIO-BYTES"))
	}))
	defer srvB.Close()

	// baseURL = srvA; a URL baixada aponta para srvB e deve permanecer
	// intocada (ex. presigned URL do MinIO, cuja assinatura quebraria se o
	// host fosse reescrito para srvA).
	c := NewClient(srvA.URL, "tok", "1")
	data, _, err := c.DownloadBytes(srvB.URL + "/x")
	if err != nil {
		t.Fatalf("DownloadBytes: %v", err)
	}
	if !hitB {
		t.Fatalf("expected server B to receive the request")
	}
	if string(data) != "MINIO-BYTES" {
		t.Fatalf("bad download: %q", data)
	}
}
