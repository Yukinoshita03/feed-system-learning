package account

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type AccountRepository struct {
	db             *gorm.DB
	redis          *redis.Client
	accessTokenTTL time.Duration
}

func NewAccountRepository(db *gorm.DB, redisClient *redis.Client, accessTokenTTL time.Duration) *AccountRepository {
	return &AccountRepository{
		db:             db,
		redis:          redisClient,
		accessTokenTTL: accessTokenTTL,
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
	return r.updateSession(ctx, id, map[string]any{
		"username":          newUsername,
		"access_token_hash": newAccessHash,
	}, newAccessHash, func(current *Account) bool {
		return current.AccessTokenHash != nil && *current.AccessTokenHash == oldAccessHash
	})
}

// ChangePassword revokes the current access and refresh credentials in the
// same database update as the password change.
func (r *AccountRepository) ChangePassword(ctx context.Context, id uint, currentPasswordHash, newPasswordHash string) error {
	return r.updateSession(ctx, id, map[string]any{
		"password":           newPasswordHash,
		"access_token_hash":  nil,
		"refresh_token_hash": nil,
		"refresh_expires_at": nil,
	}, "", func(current *Account) bool {
		return current.Password == currentPasswordHash
	})
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

// SaveSession 保存账号的当前凭证，并缓存访问 token 的哈希。
func (r *AccountRepository) SaveSession(ctx context.Context, id uint, accessHash, refreshHash string, refreshExpiresAt time.Time) error {
	return r.updateSession(ctx, id, map[string]any{
		"access_token_hash":  accessHash,
		"refresh_token_hash": refreshHash,
		"refresh_expires_at": refreshExpiresAt,
	}, accessHash, nil)
}

// IsAccessTokenActive 优先读取可信缓存，未命中或查询失败时回源 MySQL。
func (r *AccountRepository) IsAccessTokenActive(ctx context.Context, id uint, hash string) (bool, error) {
	if cached, ok, err := r.cachedAccessHash(ctx, id); err == nil && ok {
		return cached != "" && cached == hash, nil
	}
	current, err := r.loadAccessHash(ctx, id)
	return current != "" && current == hash, err
}

// UpdateAccessTokenForRefresh 仅在刷新凭证仍有效时更新访问凭证。
func (r *AccountRepository) UpdateAccessTokenForRefresh(ctx context.Context, id uint, refreshHash, accessHash string) error {
	return r.updateSession(ctx, id, map[string]any{"access_token_hash": accessHash}, accessHash, func(current *Account) bool {
		// 等待行锁后再次检查过期时间，避免使用请求开始时的旧时间。
		return current.RefreshTokenHash != nil && *current.RefreshTokenHash == refreshHash &&
			current.RefreshExpiresAt != nil && time.Now().Before(*current.RefreshExpiresAt) &&
			current.AccessTokenHash != nil
	})
}

// ClearSession 只清除当前请求对应的会话，避免旧请求注销新的登录。
func (r *AccountRepository) ClearSession(ctx context.Context, id uint, accessHash string) error {
	return r.updateSession(ctx, id, map[string]any{
		"access_token_hash":  nil,
		"refresh_token_hash": nil,
		"refresh_expires_at": nil,
	}, "", func(current *Account) bool {
		return current.AccessTokenHash != nil && *current.AccessTokenHash == accessHash
	})
}

func (r *AccountRepository) FindByRefreshTokenHash(ctx context.Context, hash string) (*Account, error) {
	var account Account
	result := r.db.WithContext(ctx).Where("refresh_token_hash = ?", hash).First(&account)
	if result.Error != nil {
		return nil, result.Error
	}
	return &account, nil
}
