package auth

import (
	"github.com/nikonikowuw/Zhulong/internal/database"
)

// User represents the persisted single-user account in SQLite.
type User struct {
	database.BaseModel
	Username     string `gorm:"column:username;unique;not null"`
	PasswordHash string `gorm:"column:password_hash;not null"`
}

// TableName returns the table name for GORM.
func (User) TableName() string {
	return "users"
}
