package chatwoot_producer

import "testing"

func envelope(sender, chat, pushName, text, id string, fromMe bool) []byte {
	return []byte(`{
		"event":"Message",
		"instanceId":"inst-1",
		"data":{
			"Info":{"Sender":"` + sender + `","Chat":"` + chat + `","PushName":"` + pushName + `","ID":"` + id + `","IsFromMe":` + boolStr(fromMe) + `},
			"Message":{"conversation":"` + text + `"}
		}
	}`)
}
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestParseIncomingText_Valid(t *testing.T) {
	msg, ok := parseIncomingText(envelope("5511988880001@s.whatsapp.net", "5511988880001@s.whatsapp.net", "João", "olá", "wamid.A", false))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if msg.JID != "5511988880001@s.whatsapp.net" || msg.PushName != "João" || msg.Text != "olá" || msg.Wamid != "wamid.A" {
		t.Fatalf("bad parse: %+v", msg)
	}
}

func TestParseIncomingText_IgnoresFromMe(t *testing.T) {
	if _, ok := parseIncomingText(envelope("x@s.whatsapp.net", "x@s.whatsapp.net", "me", "oi", "wamid.B", true)); ok {
		t.Fatal("expected ok=false for FromMe")
	}
}

func TestParseIncomingText_IgnoresGroup(t *testing.T) {
	if _, ok := parseIncomingText(envelope("x@s.whatsapp.net", "123-456@g.us", "grp", "oi", "wamid.C", false)); ok {
		t.Fatal("expected ok=false for group chat")
	}
}

func TestParseIncomingText_IgnoresNonMessage(t *testing.T) {
	if _, ok := parseIncomingText([]byte(`{"event":"Receipt","instanceId":"i","data":{}}`)); ok {
		t.Fatal("expected ok=false for non-Message event")
	}
}
