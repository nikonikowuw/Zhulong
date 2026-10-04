package database

import "time"

// BaseModel provides standard 64-bit integer primary keys and UTC audit timestamps.
// Unlike gorm.Model, it omits DeletedAt to prevent unintended soft-delete queries
// on schemas without a deleted_at column.
type BaseModel struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updatedAt"`
}
