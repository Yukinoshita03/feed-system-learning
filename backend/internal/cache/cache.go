package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client 提供业务数据的旁路缓存；Redis 读写失败时由调用方继续使用 MySQL。
// Redis 中的版本标识协调缓存回填和失效，不是 MySQL 与 Redis 的分布式事务。
type Client struct {
	redis      *redis.Client
	config     Config
	versionTTL time.Duration
}

// Entry 保存读取时的版本，后续只能在版本没有变化时回填。
type Entry struct {
	key     string
	version string
	value   string
}

func New(client *redis.Client, config Config) (*Client, error) {
	if !config.valid() {
		return nil, fmt.Errorf("业务缓存的 TTL 和超时必须是至少 1ms 的有效时长")
	}
	return &Client{redis: client, config: config, versionTTL: 2 * max(config.VideoTTL, config.AuthorTTL, config.FeedTTL)}, nil
}

func (c *Client) Config() Config { return c.config }

// 同一次 Lua 中读取版本和值；版本到期时丢弃旧值并创建新的随机版本，避免 ABA。
var loadScript = redis.NewScript(`
local result = {}
for i = 1, #KEYS, 2 do
    local version = redis.call('GET', KEYS[i + 1])
    if not version then
        version = ARGV[1]
        redis.call('SET', KEYS[i + 1], version, 'PX', ARGV[2])
        redis.call('DEL', KEYS[i])
    end
    result[#result + 1] = version
    result[#result + 1] = redis.call('GET', KEYS[i]) or false
end
return result
`)

var storeScript = redis.NewScript(`
local stored = 0
for i = 1, #KEYS, 2 do
    local n = (i + 1) / 2
    if redis.call('GET', KEYS[i + 1]) == ARGV[2 * n + 1] then
        redis.call('SET', KEYS[i], ARGV[2 * n + 2], 'PX', ARGV[1])
        redis.call('PEXPIRE', KEYS[i + 1], ARGV[2])
        stored = stored + 1
    end
end
return stored
`)

var invalidateScript = redis.NewScript(`
for i = 1, #KEYS, 2 do
    redis.call('DEL', KEYS[i])
    redis.call('SET', KEYS[i + 1], ARGV[1], 'PX', ARGV[2])
end
return 1
`)

func nonce() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (e Entry) Decode(target any) bool {
	if e.value == "" {
		return false
	}
	if err := json.Unmarshal([]byte(e.value), target); err != nil {
		log.Printf("缓存 %s 内容无法解析，回源 MySQL：%v", e.key, err)
		return false
	}
	return true
}

// LoadMany 批量读取详情时只进行一次 Redis 往返，避免列表逐条查缓存。
// Redis 不可用时返回没有版本的 Entry，这一轮数据库结果不会无条件写回缓存。
func (c *Client) LoadMany(ctx context.Context, keys []string) []Entry {
	entries := make([]Entry, len(keys))
	if c == nil || c.redis == nil || len(keys) == 0 {
		return entries
	}
	version, err := nonce()
	if err != nil {
		log.Printf("创建缓存版本失败：%v", err)
		return entries
	}
	pairs := keyPairs(keys)
	cacheCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	values, err := loadScript.Run(cacheCtx, c.redis, pairs, version, c.versionTTL.Milliseconds()).Slice()
	if err != nil || len(values) != 2*len(keys) {
		log.Printf("读取业务缓存失败，回源 MySQL：%v", err)
		return entries
	}
	for i, key := range keys {
		entries[i].key = key
		entries[i].version, _ = values[2*i].(string)
		entries[i].value, _ = values[2*i+1].(string)
	}
	return entries
}

func (c *Client) Load(ctx context.Context, key string, target any) (Entry, bool) {
	entry := c.LoadMany(ctx, []string{key})[0]
	return entry, entry.Decode(target)
}

func (c *Client) Store(ctx context.Context, entry Entry, ttl time.Duration, value any) bool {
	return c.StoreMany(ctx, []Entry{entry}, ttl, []any{value}) == 1
}

func (c *Client) StoreMany(ctx context.Context, entries []Entry, ttl time.Duration, values []any) int {
	if c == nil || c.redis == nil || ttl < time.Millisecond || len(entries) != len(values) {
		return 0
	}
	keys := make([]string, 0, 2*len(entries))
	args := []any{ttl.Milliseconds(), c.versionTTL.Milliseconds()}
	for i, entry := range entries {
		if entry.key == "" || entry.version == "" {
			continue
		}
		data, err := json.Marshal(values[i])
		if err != nil {
			log.Printf("序列化缓存 %s 失败：%v", entry.key, err)
			continue
		}
		keys = append(keys, entry.key, versionKey(entry.key))
		args = append(args, entry.version, string(data))
	}
	if len(keys) == 0 {
		return 0
	}
	cacheCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	stored, err := storeScript.Run(cacheCtx, c.redis, keys, args...).Int()
	if err != nil {
		log.Printf("回填业务缓存失败，继续使用 MySQL 结果：%v", err)
		return 0
	}
	return stored
}

// Invalidate 在同一个 Lua 中删除值、替换版本，使较早的数据库查询无法回填旧值。
func (c *Client) Invalidate(ctx context.Context, keys ...string) error {
	if c == nil || c.redis == nil || len(keys) == 0 {
		return nil
	}
	version, err := nonce()
	if err != nil {
		return err
	}
	cacheCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	return invalidateScript.Run(cacheCtx, c.redis, keyPairs(keys), version, c.versionTTL.Milliseconds()).Err()
}

func keyPairs(keys []string) []string {
	pairs := make([]string, 0, 2*len(keys))
	for _, key := range keys {
		pairs = append(pairs, key, versionKey(key))
	}
	return pairs
}

// Forget 删除一个标记。业务写入失败时调用，让调用方可以立刻重试，
// 而不是被自己的标记挡到窗口过期。使用 WithoutCancel：客户端取消请求时
// 同样要清理，否则脏标记会一直留到 TTL 到期。
func (c *Client) Forget(ctx context.Context, key string) {
	if c == nil || c.redis == nil || key == "" {
		return
	}
	cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.config.Timeout)
	defer cancel()
	if err := c.redis.Del(cacheCtx, key).Err(); err != nil {
		log.Printf("删除去重标记失败，窗口到期后自动收敛：%v", err)
	}
}

// MarkOnce 尝试在 ttl 内独占一个标记，返回 true 表示这是第一次。
//
// 用 SETNX 而不是"先 GET 再 SET"：后者在两个请求同时到达时会都读到"没有"，
// 于是各写一条播放记录，去重失效。SETNX 的判定和写入在 Redis 内部是原子的。
//
// Redis 不可用时返回 true，让调用方继续写 MySQL：播放量宁可重复计数，
// 也不能因为缓存故障把数据丢掉。
func (c *Client) MarkOnce(ctx context.Context, key string, ttl time.Duration) bool {
	if c == nil || c.redis == nil || key == "" || ttl < time.Millisecond {
		return true
	}
	cacheCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	created, err := c.redis.SetNX(cacheCtx, key, "1", ttl).Result()
	if err != nil {
		log.Printf("写入去重标记失败，按首次播放处理：%v", err)
		return true
	}
	return created
}
