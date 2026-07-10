package chatwoot_repository

import (
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ChatwootConfigRepository interface {
	Get() (*chatwoot_model.ChatwootConfig, error)
	Save(cfg *chatwoot_model.ChatwootConfig) error
}

type chatwootConfigRepository struct {
	db *gorm.DB
}

func NewChatwootConfigRepository(db *gorm.DB) ChatwootConfigRepository {
	return &chatwootConfigRepository{db: db}
}

// Get retorna a config singleton, ou (nil, nil) se ainda não existe.
func (r *chatwootConfigRepository) Get() (*chatwoot_model.ChatwootConfig, error) {
	var cfg chatwoot_model.ChatwootConfig
	err := r.db.First(&cfg).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save mantém sempre uma única linha (ID=1).
func (r *chatwootConfigRepository) Save(cfg *chatwoot_model.ChatwootConfig) error {
	cfg.ID = 1
	return r.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(cfg).Error
}
