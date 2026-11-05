package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 先标记更新中并删除旧值。标记不设 TTL，失败后由下一次数据库回源修复。
var beginSessionUpdateScript = redis.NewScript(`
redis.call('SET', KEYS[2], ARGV[1])
redis.call('DEL', KEYS[1])
return 1
`)

// 提交后的更新只认自己的标记，防止晚到的旧请求覆盖或删除新登录态。
// 空标记仅用于持有数据库行锁时的缓存回填。
var publishSessionScript = redis.NewScript(`
if ARGV[1] ~= '' and redis.call('GET', KEYS[2]) ~= ARGV[1] then
    return 0
end
if ARGV[2] == '' then
    redis.call('DEL', KEYS[1], KEYS[2])
else
    redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
    redis.call('SET', KEYS[2], 'ready', 'PX', ARGV[3])
end
return 1
`)

func sessionCacheKeys(id uint) []string {
	return []string{
		fmt.Sprintf("auth:access:%d", id),
		fmt.Sprintf("auth:access:state:%d", id),
	}
}

// cachedAccessHash 一次读取值和状态，仅信任已经完整发布的缓存。
// 旧版本只写了哈希、没有 ready 状态的条目，也会回源数据库确认。
func (r *AccountRepository) cachedAccessHash(ctx context.Context, id uint) (string, bool, error) {
	if r.redis == nil {
		return "", false, errors.New("Redis 客户端未配置")
	}
	values, err := r.redis.MGet(ctx, sessionCacheKeys(id)...).Result()
	if err != nil {
		return "", false, err
	}
	if values[1] != "ready" {
		return "", false, nil
	}
	hash, ok := values[0].(string)
	return hash, ok, nil
}

func (r *AccountRepository) beginSessionUpdate(ctx context.Context, id uint) (string, error) {
	if r.redis == nil {
		return "", errors.New("Redis 客户端未配置")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	marker := "updating:" + hex.EncodeToString(nonce)
	if err := beginSessionUpdateScript.Run(ctx, r.redis, sessionCacheKeys(id), marker).Err(); err != nil {
		return "", fmt.Errorf("使账号 %d 的旧登录态缓存失效失败：%w", id, err)
	}
	return marker, nil
}

func (r *AccountRepository) publishSession(ctx context.Context, id uint, marker, accessHash string) error {
	if r.redis == nil {
		return errors.New("Redis 客户端未配置")
	}
	if accessHash != "" && r.accessTokenTTL.Milliseconds() <= 0 {
		return errors.New("访问凭证缓存有效期必须大于 0 毫秒")
	}
	return publishSessionScript.Run(ctx, r.redis, sessionCacheKeys(id), marker, accessHash, r.accessTokenTTL.Milliseconds()).Err()
}

// updateSession 统一协调登录、刷新、改名、退出和改密码。
// 行锁协调数据库更新与缓存回填；更新前的 Redis 标记阻止旧缓存被鉴权信任。
func (r *AccountRepository) updateSession(
	ctx context.Context,
	id uint,
	updates map[string]any,
	accessHash string,
	valid func(*Account) bool,
) error {
	var marker string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, id).Error; err != nil {
			return err
		}
		if valid != nil && !valid(&current) {
			return gorm.ErrRecordNotFound
		}
		var err error
		marker, err = r.beginSessionUpdate(ctx, id)
		if err != nil {
			// 无法确认旧缓存失效时，不修改数据库里的登录态。
			return err
		}
		return tx.Model(&Account{}).Where("id = ?", id).Updates(updates).Error
	})
	if err != nil {
		// 若标记已写入，保留更新中状态，下一次鉴权会回源并修复。
		return err
	}
	if err := r.publishSession(ctx, id, marker, accessHash); err != nil {
		// 数据库已提交：保留更新中标记，让鉴权回源，而不是信任旧值。
		log.Printf("发布账号 %d 的登录态缓存失败，后续鉴权回源 MySQL：%v", id, err)
	}
	return nil
}

// loadAccessHash 缓存未命中、更新中或 Redis 出错时，从 MySQL 确认并回填。
// 回填期间持有相同账号行锁，避免旧查询在退出完成后重新写入旧凭证。
func (r *AccountRepository) loadAccessHash(ctx context.Context, id uint) (string, error) {
	var accessHash string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current struct {
			AccessTokenHash *string
		}
		err := tx.Model(&Account{}).Select("access_token_hash").
			Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(&current).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if current.AccessTokenHash != nil {
			accessHash = *current.AccessTokenHash
		}
		if err := r.publishSession(ctx, id, "", accessHash); err != nil {
			log.Printf("回填账号 %d 的登录态缓存失败，使用 MySQL 查询结果：%v", id, err)
		}
		return nil
	})
	return accessHash, err
}
