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
	jid, text, _, _, _, ok := shouldForward(body)
	if !ok || jid != "5511988880001@s.whatsapp.net" || text != "bom dia!" {
		t.Fatalf("expected forward; got jid=%q text=%q ok=%v", jid, text, ok)
	}
}

func TestShouldForward_IgnoresIncoming(t *testing.T) {
	body := []byte(`{"event":"message_created","message_type":"incoming","private":false,"content":"olá","conversation":{"contact_inbox":{"source_id":"x"}}}`)
	if _, _, _, _, _, ok := shouldForward(body); ok {
		t.Fatal("expected ok=false for incoming (anti-echo)")
	}
}

func TestShouldForward_IgnoresPrivateNote(t *testing.T) {
	body := []byte(`{"event":"message_created","message_type":"outgoing","private":true,"content":"nota","conversation":{"contact_inbox":{"source_id":"x"}}}`)
	if _, _, _, _, _, ok := shouldForward(body); ok {
		t.Fatal("expected ok=false for private note")
	}
}

func TestShouldForward_WithAttachment(t *testing.T) {
	body := []byte(`{
		"event":"message_created","message_type":"outgoing","private":false,"content":"veja",
		"conversation":{"contact_inbox":{"source_id":"5511988880001@s.whatsapp.net"}},
		"attachments":[{"data_url":"http://localhost:3100/rails/x/foto.png","file_type":"image","extension":"png","content_type":"image/png"}]
	}`)
	jid, text, atts, _, _, ok := shouldForward(body)
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
	_, text, atts, _, _, ok := shouldForward(body)
	if !ok || text != "oi" || len(atts) != 0 {
		t.Fatalf("texto puro quebrou: text=%q atts=%d ok=%v", text, len(atts), ok)
	}
}

func TestShouldForward_ExtractsIds(t *testing.T) {
	body := []byte(`{
		"event":"message_created","message_type":"outgoing","private":false,"content":"oi",
		"id":11,
		"conversation":{"id":2,"contact_inbox":{"source_id":"5511988880001@s.whatsapp.net"}}
	}`)
	jid, text, atts, mid, cid, ok := shouldForward(body)
	if !ok || jid == "" || text != "oi" || len(atts) != 0 || mid != 11 || cid != 2 {
		t.Fatalf("bad: mid=%d cid=%d ok=%v", mid, cid, ok)
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
