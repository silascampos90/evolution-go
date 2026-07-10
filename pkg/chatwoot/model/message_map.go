package chatwoot_model

// MessageMap correlaciona o id de uma mensagem enviada no WhatsApp (wamid) com a
// mensagem correspondente no Chatwoot, para propagar status de entrega/leitura.
type MessageMap struct {
	Wamid          string `json:"wamid" gorm:"primaryKey"`
	ConversationID int    `json:"conversationId"`
	MessageID      int    `json:"messageId"`
	InstanceID     string `json:"instanceId"`
}

func (MessageMap) TableName() string { return "message_maps" }
