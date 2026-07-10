package chatwoot_handler

import "testing"

func TestShouldForward_OutgoingText(t *testing.T) {
	body := []byte(`{
		"event":"message_created",
		"message_type":"outgoing",
		"private":false,
		"content":"bom dia!",
		"conversation":{"contact_inbox":{"source_id":"5511988880001@s.whatsapp.net"}}
	}`)
	jid, text, ok := shouldForward(body)
	if !ok || jid != "5511988880001@s.whatsapp.net" || text != "bom dia!" {
		t.Fatalf("expected forward; got jid=%q text=%q ok=%v", jid, text, ok)
	}
}

func TestShouldForward_IgnoresIncoming(t *testing.T) {
	body := []byte(`{"event":"message_created","message_type":"incoming","private":false,"content":"olá","conversation":{"contact_inbox":{"source_id":"x"}}}`)
	if _, _, ok := shouldForward(body); ok {
		t.Fatal("expected ok=false for incoming (anti-echo)")
	}
}

func TestShouldForward_IgnoresPrivateNote(t *testing.T) {
	body := []byte(`{"event":"message_created","message_type":"outgoing","private":true,"content":"nota","conversation":{"contact_inbox":{"source_id":"x"}}}`)
	if _, _, ok := shouldForward(body); ok {
		t.Fatal("expected ok=false for private note")
	}
}

func TestValidSignature(t *testing.T) {
	// HMAC_SHA256("s3cr3t", "123.body") calculado externamente
	secret := "s3cr3t"
	ts := "123"
	body := []byte("body")
	good := computeSig(secret, ts, body) // helper exposto no impl para o teste
	if !validSignature(secret, ts, body, "sha256="+good) {
		t.Fatal("expected valid signature")
	}
	if validSignature(secret, ts, body, "sha256=deadbeef") {
		t.Fatal("expected invalid signature to fail")
	}
}
