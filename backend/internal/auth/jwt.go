package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenManager 保存签名密钥和注入的访问凭证有效期。
type TokenManager struct {
	secret         []byte
	accessTokenTTL time.Duration
}

// Claims 是 token 中保存的账号信息和标准时间字段。
type Claims struct {
	AccountID uint   `json:"account_id"`
	Username  string `json:"username"`
	jwt.RegisteredClaims
}

// NewTokenManager 在未配置密钥时为当前进程生成临时密钥。
func NewTokenManager(secret string, accessTokenTTL time.Duration) (*TokenManager, bool, error) {
	if accessTokenTTL < time.Second {
		return nil, false, errors.New("access token 有效期至少为 1 秒")
	}
	if secret != "" {
		return &TokenManager{secret: []byte(secret), accessTokenTTL: accessTokenTTL}, false, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	return &TokenManager{secret: key, accessTokenTTL: accessTokenTTL}, true, nil
}

// GenerateToken 按注入的有效期签发访问 JWT。
func (m *TokenManager) GenerateToken(accountID uint, username string) (string, error) {
	// 保证同一秒连续登录或刷新时，签发的 token 也能互相区分。
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return "", err
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"account_id": accountID,
		"username":   username,
		"jti":        hex.EncodeToString(identifier),
		"exp":        now.Add(m.accessTokenTTL).Unix(),
		"iat":        now.Unix(),
		"nbf":        now.Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// GenerateRefreshToken 生成随机刷新凭证；数据库只保存它的哈希。
func GenerateRefreshToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func HashRefreshToken(token string) string { return HashToken(token) }

// ParseToken 校验签名、算法和有效期，并读取账号 ID。
func (m *TokenManager) ParseToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if !token.Valid || claims.AccountID == 0 {
		return nil, errors.New("无效的 token 信息")
	}
	return claims, nil
}
