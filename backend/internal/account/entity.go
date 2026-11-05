package account

import "time"

type Account struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	Username         string     `gorm:"unique" json:"username"`
	Password         string     `json:"-"`
	AvatarURL        string     `gorm:"size:512" json:"avatar_url,omitempty"`
	Bio              string     `gorm:"size:255" json:"bio,omitempty"`
	AccessTokenHash  *string    `gorm:"size:64" json:"-"`
	RefreshTokenHash *string    `gorm:"size:64;uniqueIndex" json:"-"`
	RefreshExpiresAt *time.Time `json:"-"`
}

type RenameRequest struct {
	NewUsername string `json:"new_username"`
}

type FindByIDRequest struct {
	ID uint `json:"id"`
}

type FindByUsernameRequest struct {
	Username string `json:"username"`
}

type ChangePasswordRequest struct {
	Username    string `json:"username"`
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// UpdateProfileRequest uses pointers so callers can distinguish an omitted
// field from an explicitly empty value that clears the field.
type UpdateProfileRequest struct {
	AvatarURL *string `json:"avatar_url"`
	Bio       *string `json:"bio"`
}
