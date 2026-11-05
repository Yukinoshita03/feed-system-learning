package account

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"feed-system-learning/internal/auth"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var ErrUsernameTaken = errors.New("用户名已存在")

const mysqlDuplicateEntry uint16 = 1062

var ErrInvalidCredentials = errors.New("用户名或密码错误")
var ErrInvalidRefreshToken = errors.New("刷新 token 无效或已过期")
var ErrSessionExpired = errors.New("登录状态已失效")
var ErrNewUsernameRequired = errors.New("新用户名不能为空")
var ErrUsernameTooLong = errors.New("用户名不能超过 191 个字符")
var ErrPasswordInvalid = errors.New("密码不能为空，且不能超过 72 字节")
var ErrPasswordFieldsRequired = errors.New("用户名、旧密码和新密码不能为空")
var ErrProfileEmpty = errors.New("没有提供要更新的资料")
var ErrAvatarURLInvalid = errors.New("头像地址格式不正确")
var ErrBioTooLong = errors.New("个人简介不能超过 255 个字符")

const refreshTokenLifetime = 7 * 24 * time.Hour
const maxUsernameRunes = 191
const maxPasswordBytes = 72
const maxBioRunes = 255
const maxAvatarURLBytes = 512

type AccountService struct {
	repo   *AccountRepository
	tokens *auth.TokenManager
}

func NewAccountService(repo *AccountRepository, tokens *auth.TokenManager) *AccountService {
	return &AccountService{
		repo:   repo,
		tokens: tokens,
	}
}
func (s *AccountService) ValidateRegistration(username, password string) error {
	if strings.TrimSpace(username) == "" {
		return errors.New("用户名不能为空")
	}
	if utf8.RuneCountInString(username) > maxUsernameRunes {
		return ErrUsernameTooLong
	}
	if password == "" {
		return errors.New("密码不能为空")
	}
	if len([]byte(password)) > maxPasswordBytes {
		return ErrPasswordInvalid
	}
	return nil
}
func (s *AccountService) CreateAccount(
	ctx context.Context,
	username string,
	password string,
) error {
	// 1. 检查用户名和密码
	if err := s.ValidateRegistration(username, password); err != nil {
		return err
	}

	// 2. 对密码做哈希
	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return err
	}

	// 3. 创建账号对象，保存的是哈希值
	account := &Account{
		Username: username,
		Password: string(passwordHash),
	}

	err = s.repo.CreateAccount(ctx, account)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlDuplicateEntry {
			return ErrUsernameTaken
		}
		return err
	}
	return nil
}

type LoginResult struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    uint   `json:"account_id"`
	Username     string `json:"username"`
}

func (s *AccountService) Login(
	ctx context.Context,
	username string,
	password string,
) (*LoginResult, error) {

	account, err := s.repo.FindByUsername(ctx, username)
	// 用户不存在
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidCredentials
	}
	// 其他查询错误
	if err != nil {
		return nil, err
	}
	// 2. 用数据库中的哈希，验证用户输入的密码
	err = bcrypt.CompareHashAndPassword(
		[]byte(account.Password), // 数据库中的密码哈希
		[]byte(password),         // 用户输入的密码
	)
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	// 密码验证通过后，生成包含账号信息的访问 token。
	token, err := s.tokens.GenerateToken(account.ID, account.Username)
	if err != nil {
		return nil, err
	}
	// 刷新凭证只返回给客户端；数据库保存哈希和过期时间。
	refreshToken, err := auth.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}
	err = s.repo.SaveSession(ctx, account.ID, auth.HashToken(token), auth.HashRefreshToken(refreshToken), time.Now().Add(refreshTokenLifetime))
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, RefreshToken: refreshToken, AccountID: account.ID, Username: account.Username}, nil
}

// RefreshAccessToken 使用有效的刷新凭证换取新的访问 token。
func (s *AccountService) RefreshAccessToken(ctx context.Context, refreshToken string) (*LoginResult, error) {
	refreshHash := auth.HashRefreshToken(refreshToken)
	account, err := s.repo.FindByRefreshTokenHash(ctx, refreshHash)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidRefreshToken
	}
	if err != nil {
		return nil, err
	}
	if account.RefreshExpiresAt == nil || !time.Now().Before(*account.RefreshExpiresAt) {
		return nil, ErrInvalidRefreshToken
	}
	token, err := s.tokens.GenerateToken(account.ID, account.Username)
	if err != nil {
		return nil, err
	}
	// 防止退出登录后，先前已开始的刷新请求再次写入访问凭证。
	err = s.repo.UpdateAccessTokenForRefresh(ctx, account.ID, refreshHash, auth.HashToken(token))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidRefreshToken
	}
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, AccountID: account.ID, Username: account.Username}, nil
}

func (s *AccountService) Logout(ctx context.Context, accountID uint, accessHash string) error {
	err := s.repo.ClearSession(ctx, accountID, accessHash)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrSessionExpired
	}
	return err
}

// GetByID 查询当前登录账号，数据库查询由 Repo 负责。
func (s *AccountService) GetByID(ctx context.Context, id uint) (*Account, error) {
	return s.repo.FindByID(ctx, id)
}

// GetByUsername returns an account for public profile lookup.
func (s *AccountService) GetByUsername(ctx context.Context, username string) (*Account, error) {
	return s.repo.FindByUsername(ctx, strings.TrimSpace(username))
}

// Rename updates the account name and rotates the active access token.
func (s *AccountService) Rename(ctx context.Context, accountID uint, oldAccessHash, newUsername string) (string, error) {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return "", ErrNewUsernameRequired
	}
	if utf8.RuneCountInString(newUsername) > maxUsernameRunes {
		return "", ErrUsernameTooLong
	}

	token, err := s.tokens.GenerateToken(accountID, newUsername)
	if err != nil {
		return "", err
	}
	err = s.repo.RenameWithToken(ctx, accountID, oldAccessHash, newUsername, auth.HashToken(token))
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlDuplicateEntry {
			return "", ErrUsernameTaken
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrSessionExpired
		}
		return "", err
	}
	return token, nil
}

// ChangePassword verifies the old password, stores a bcrypt hash for the new
// one, and revokes all existing access and refresh credentials atomically.
func (s *AccountService) ChangePassword(ctx context.Context, username, oldPassword, newPassword string) error {
	username = strings.TrimSpace(username)
	if username == "" || oldPassword == "" || newPassword == "" {
		return ErrPasswordFieldsRequired
	}
	if len([]byte(newPassword)) > maxPasswordBytes {
		return ErrPasswordInvalid
	}
	account, err := s.repo.FindByUsername(ctx, username)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(oldPassword)); err != nil {
		return ErrInvalidCredentials
	}

	newPasswordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	err = s.repo.ChangePassword(ctx, account.ID, account.Password, string(newPasswordHash))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// The password changed after the lookup; do not overwrite the newer value.
		return ErrInvalidCredentials
	}
	return err
}

func (s *AccountService) UpdateAvatar(ctx context.Context, accountID uint, avatarURL string) error {
	return s.repo.UpdateAvatar(ctx, accountID, avatarURL)
}

// UpdateProfile updates only fields present in the request. A provided empty
// string clears that field.
func (s *AccountService) UpdateProfile(ctx context.Context, accountID uint, req UpdateProfileRequest) error {
	updates := make(map[string]any, 2)
	if req.AvatarURL != nil {
		avatarURL, err := normalizeAvatarURL(*req.AvatarURL)
		if err != nil {
			return err
		}
		updates["avatar_url"] = avatarURL
	}
	if req.Bio != nil {
		bio := strings.TrimSpace(*req.Bio)
		if utf8.RuneCountInString(bio) > maxBioRunes {
			return ErrBioTooLong
		}
		updates["bio"] = bio
	}
	if len(updates) == 0 {
		return ErrProfileEmpty
	}
	return s.repo.UpdateProfile(ctx, accountID, updates)
}

func normalizeAvatarURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len([]byte(value)) > maxAvatarURLBytes {
		return "", ErrAvatarURLInvalid
	}
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", ErrAvatarURLInvalid
	}
	if parsed.IsAbs() {
		if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", ErrAvatarURLInvalid
		}
		return value, nil
	}
	if !strings.HasPrefix(value, "/static/") {
		return "", ErrAvatarURLInvalid
	}
	return value, nil
}
