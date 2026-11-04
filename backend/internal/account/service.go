package account

import (
	"context"
	"errors"
	"feed-system-learning/internal/auth"
	"gorm.io/gorm"
	"time"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

var ErrUsernameTaken = errors.New("用户名已存在")

const mysqlDuplicateEntry uint16 = 1062

var ErrInvalidCredentials = errors.New("用户名或密码错误")
var ErrInvalidRefreshToken = errors.New("刷新 token 无效或已过期")
var ErrSessionExpired = errors.New("登录状态已失效")

const refreshTokenLifetime = 7 * 24 * time.Hour

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
	if username == "" {
		return errors.New("用户名不能为空")
	}
	if password == "" {
		return errors.New("密码不能为空")
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
	err = s.repo.UpdateAccessTokenForRefresh(ctx, account.ID, refreshHash, auth.HashToken(token), time.Now())
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
