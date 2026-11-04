package account

import "time"

type Account struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	Username         string     `gorm:"unique" json:"username"`
	Password         string     `json:"-"`
	AccessTokenHash  *string    `gorm:"size:64" json:"-"`
	RefreshTokenHash *string    `gorm:"size:64;uniqueIndex" json:"-"`
	RefreshExpiresAt *time.Time `json:"-"`
}
