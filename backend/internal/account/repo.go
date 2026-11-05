package account

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type AccountRepository struct {
	db *gorm.DB
}

func NewAccountRepository(db *gorm.DB) *AccountRepository {
	return &AccountRepository{
		db: db,
	}
}
func (r *AccountRepository) CreateAccount(ctx context.Context, account *Account) error {
	result := r.db.WithContext(ctx).Create(account)
	return result.Error
}
func (r *AccountRepository) FindByUsername(
	ctx context.Context,
	username string,
) (*Account, error) {
	var account Account
	result := r.db.WithContext(ctx).
		Where("username = ?", username).
		First(&account)
	if result.Error != nil {
		return nil, result.Error
	}
	return &account, nil
}

func (r *AccountRepository) FindByID(ctx context.Context, id uint) (*Account, error) {
	var account Account
	result := r.db.WithContext(ctx).First(&account, id)
	if result.Error != nil {
		return nil, result.Error
	}
	return &account, nil
}

// RenameWithToken changes the username and active access token together.
// Matching the previous token hash prevents an old request from replacing a
// newer session after the user has logged in elsewhere.
func (r *AccountRepository) RenameWithToken(ctx context.Context, id uint, oldAccessHash, newUsername, newAccessHash string) error {
	result := r.db.WithContext(ctx).Model(&Account{}).
		Where("id = ? AND access_token_hash = ?", id, oldAccessHash).
		Updates(map[string]any{
			"username":          newUsername,
			"access_token_hash": newAccessHash,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ChangePassword revokes the current access and refresh credentials in the
// same database update as the password change.
func (r *AccountRepository) ChangePassword(ctx context.Context, id uint, currentPasswordHash, newPasswordHash string) error {
	result := r.db.WithContext(ctx).Model(&Account{}).
		Where("id = ? AND password = ?", id, currentPasswordHash).
		Updates(map[string]any{
			"password":           newPasswordHash,
			"access_token_hash":  nil,
			"refresh_token_hash": nil,
			"refresh_expires_at": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) UpdateAvatar(ctx context.Context, id uint, avatarURL string) error {
	result := r.db.WithContext(ctx).Model(&Account{}).
		Where("id = ?", id).
		Update("avatar_url", avatarURL)
	return result.Error
}

func (r *AccountRepository) UpdateProfile(ctx context.Context, id uint, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&Account{}).
		Where("id = ?", id).
		Updates(fields).Error
}

// SaveSession 同时替换账号的访问和刷新凭证。
func (r *AccountRepository) SaveSession(ctx context.Context, id uint, accessHash, refreshHash string, expiresAt time.Time) error {
	result := r.db.WithContext(ctx).Model(&Account{}).Where("id = ?", id).Updates(map[string]any{
		"access_token_hash":  accessHash,
		"refresh_token_hash": refreshHash,
		"refresh_expires_at": expiresAt,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// IsAccessTokenActive 检查 token 是否仍是此账号当前的访问凭证。
func (r *AccountRepository) IsAccessTokenActive(ctx context.Context, id uint, hash string) (bool, error) {
	var count int64
	result := r.db.WithContext(ctx).Model(&Account{}).Where("id = ? AND access_token_hash = ?", id, hash).Count(&count)
	return count > 0, result.Error
}

// UpdateAccessTokenForRefresh 仅在刷新凭证仍有效时更新访问凭证。
func (r *AccountRepository) UpdateAccessTokenForRefresh(ctx context.Context, id uint, refreshHash, accessHash string, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&Account{}).
		Where("id = ? AND refresh_token_hash = ? AND refresh_expires_at > ? AND access_token_hash IS NOT NULL", id, refreshHash, now).
		Update("access_token_hash", accessHash)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ClearSession 只清除当前请求对应的会话，避免旧请求注销新的登录。
func (r *AccountRepository) ClearSession(ctx context.Context, id uint, accessHash string) error {
	result := r.db.WithContext(ctx).Model(&Account{}).
		Where("id = ? AND access_token_hash = ?", id, accessHash).
		Updates(map[string]any{"access_token_hash": nil, "refresh_token_hash": nil, "refresh_expires_at": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) FindByRefreshTokenHash(ctx context.Context, hash string) (*Account, error) {
	var account Account
	result := r.db.WithContext(ctx).Where("refresh_token_hash = ?", hash).First(&account)
	if result.Error != nil {
		return nil, result.Error
	}
	return &account, nil
}
