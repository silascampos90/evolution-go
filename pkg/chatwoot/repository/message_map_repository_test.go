package chatwoot_repository

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newMockDB2(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB, WithoutReturning: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm: %v", err)
	}
	return gdb, mock
}

func TestSaveInsertsMap(t *testing.T) {
	gdb, mock := newMockDB2(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "message_maps"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewMessageMapRepository(gdb)
	err := repo.Save(&chatwoot_model.MessageMap{Wamid: "wamid.A", ConversationID: 2, MessageID: 11, InstanceID: "inst-1"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
