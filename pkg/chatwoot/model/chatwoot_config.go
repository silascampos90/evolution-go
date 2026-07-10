package chatwoot_model

// ChatwootConfig é um singleton (uma linha) com os dados de acesso à API do Chatwoot.
type ChatwootConfig struct {
	ID        uint   `json:"id" gorm:"primaryKey"`
	BaseURL   string `json:"baseUrl"`
	APIToken  string `json:"-"` // sensível: nunca serializar de volta
	AccountID string `json:"accountId"`
}

func (ChatwootConfig) TableName() string { return "chatwoot_configs" }
