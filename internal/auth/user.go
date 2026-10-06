package auth

import (
	"time"
)

// User represents the persisted single-user account in SQLite.
type User struct {
	ID           int64     `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Username     string    `gorm:"column:username;unique;not null" json:"username"`
	PasswordHash string    `gorm:"column:password_hash;not null" json:"-"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName returns the table name for GORM.
func (User) TableName() string {
	return "users"
}
