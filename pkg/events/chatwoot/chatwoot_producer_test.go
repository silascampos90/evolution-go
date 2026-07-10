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
	msg, ok := parseIncoming(envelope("5511988880001@s.whatsapp.net", "5511988880001@s.whatsapp.net", "João", "olá", "wamid.A", false))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if msg.JID != "5511988880001@s.whatsapp.net" || msg.PushName != "João" || msg.Text != "olá" || msg.Wamid != "wamid.A" {
		t.Fatalf("bad parse: %+v", msg)
	}
}

func TestParseIncomingText_IgnoresFromMe(t *testing.T) {
	if _, ok := parseIncoming(envelope("x@s.whatsapp.net", "x@s.whatsapp.net", "me", "oi", "wamid.B", true)); ok {
		t.Fatal("expected ok=false for FromMe")
	}
}

func TestParseIncomingText_IgnoresGroup(t *testing.T) {
	if _, ok := parseIncoming(envelope("x@s.whatsapp.net", "123-456@g.us", "grp", "oi", "wamid.C", false)); ok {
		t.Fatal("expected ok=false for group chat")
	}
}

func TestParseIncomingText_IgnoresNonMessage(t *testing.T) {
	if _, ok := parseIncoming([]byte(`{"event":"Receipt","instanceId":"i","data":{}}`)); ok {
		t.Fatal("expected ok=false for non-Message event")
	}
}

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

func TestParseReceipt_Delivered(t *testing.T) {
	env := []byte(`{"event":"Receipt","state":"Delivered","instanceId":"inst-1","data":{"MessageIDs":["wamid.A","wamid.B"]}}`)
	state, ids, inst, ok := parseReceipt(env)
	if !ok || state != "Delivered" || inst != "inst-1" || len(ids) != 2 || ids[0] != "wamid.A" {
		t.Fatalf("bad: state=%s ids=%v inst=%s ok=%v", state, ids, inst, ok)
	}
}

func TestParseReceipt_IgnoresNonReceipt(t *testing.T) {
	if _, _, _, ok := parseReceipt([]byte(`{"event":"Message","data":{}}`)); ok {
		t.Fatal("esperado ok=false para não-Receipt")
	}
}

func TestParseReceipt_IgnoresReadSelf(t *testing.T) {
	if _, _, _, ok := parseReceipt([]byte(`{"event":"Receipt","state":"ReadSelf","instanceId":"i","data":{"MessageIDs":["x"]}}`)); ok {
		t.Fatal("esperado ok=false para ReadSelf")
	}
}
