package chatwoot_repository

import (
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MessageMapRepository interface {
	Save(m *chatwoot_model.MessageMap) error
	Get(wamid string) (*chatwoot_model.MessageMap, error)
}

type messageMapRepository struct {
	db *gorm.DB
}

func NewMessageMapRepository(db *gorm.DB) MessageMapRepository {
	return &messageMapRepository{db: db}
}

// Save é idempotente por wamid (upsert).
func (r *messageMapRepository) Save(m *chatwoot_model.MessageMap) error {
	return r.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(m).Error
}

// Get retorna (nil, nil) quando não há mapeamento para o wamid.
func (r *messageMapRepository) Get(wamid string) (*chatwoot_model.MessageMap, error) {
	var m chatwoot_model.MessageMap
	err := r.db.Where("wamid = ?", wamid).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
