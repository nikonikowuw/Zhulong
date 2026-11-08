package auth

import "time"

// User represents the persisted single-user account in SQLite.
type User struct {
	ID           int64     `gorm:"primaryKey;autoIncrement;column:id"`
	Username     string    `gorm:"column:username;unique;not null"`
	PasswordHash string    `gorm:"column:password_hash;not null"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

// TableName returns the table name for GORM.
func (User) TableName() string {
	return "users"
}
