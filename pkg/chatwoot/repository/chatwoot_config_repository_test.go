package chatwoot_repository

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sqlmock: %v", err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB, WithoutReturning: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open gorm: %v", err)
	}
	return gdb, mock
}

func TestSaveInsertsWhenNoRow(t *testing.T) {
	gdb, mock := newMockDB(t)

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "chatwoot_configs"`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewChatwootConfigRepository(gdb)
	err := repo.Save(&chatwoot_model.ChatwootConfig{BaseURL: "http://x:3000", APIToken: "tok", AccountID: "1"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
