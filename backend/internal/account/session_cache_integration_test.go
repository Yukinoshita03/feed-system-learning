package account

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"feed-system-learning/internal/auth"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 显式指定专用 MySQL 测试库；只创建和清理本测试拥有的账号及 Redis key。
func sessionCacheFixture(t *testing.T) (*AccountRepository, Account) {
	t.Helper()
	dsn := os.Getenv("FEED_TEST_MYSQL_DSN")
	addr := os.Getenv("FEED_TEST_REDIS_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("set FEED_TEST_MYSQL_DSN and FEED_TEST_REDIS_ADDR for integration tests")
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrate(&Account{}); err != nil {
		t.Fatal(err)
	}
	redisDB := 15
	if value := os.Getenv("FEED_TEST_REDIS_DB"); value != "" {
		redisDB, err = strconv.Atoi(value)
		if err != nil || redisDB < 0 {
			t.Fatal("invalid FEED_TEST_REDIS_DB")
		}
	}
	client := redis.NewClient(&redis.Options{
		Addr: addr, Password: os.Getenv("FEED_TEST_REDIS_PASSWORD"), DB: redisDB,
		ContextTimeoutEnabled: true, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	account := Account{
		ID:       uint(1_000_000_000 + time.Now().UnixNano()%1_000_000_000),
		Username: fmt.Sprintf("cache_%d", time.Now().UnixNano()),
		Password: "original-password-hash",
	}
	if err := database.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.Del(ctx, sessionCacheKeys(account.ID)...).Err(); err != nil {
			t.Errorf("Redis cleanup: %v", err)
		}
		if err := database.Delete(&Account{}, account.ID).Error; err != nil {
			t.Errorf("MySQL cleanup: %v", err)
		}
	})
	return NewAccountRepository(database, client, 2*time.Minute), account
}

func expectAccessCache(t *testing.T, repo *AccountRepository, id uint, hash string) {
	t.Helper()
	value, ok, err := repo.cachedAccessHash(context.Background(), id)
	if err != nil || !ok || value != hash {
		t.Fatalf("cache = %q, hit = %v, err = %v; want %q", value, ok, err, hash)
	}
	for _, key := range sessionCacheKeys(id) {
		ttl, err := repo.redis.PTTL(context.Background(), key).Result()
		if err != nil || ttl <= 0 || ttl > repo.accessTokenTTL {
			t.Fatalf("unexpected TTL for %s: %v, %v", key, ttl, err)
		}
	}
}

func expectActive(t *testing.T, repo *AccountRepository, id uint, hash string, want bool) {
	t.Helper()
	active, err := repo.IsAccessTokenActive(context.Background(), id, hash)
	if err != nil || active != want {
		t.Fatalf("active = %v, err = %v; want %v", active, err, want)
	}
}

func expectNoAccessCache(t *testing.T, repo *AccountRepository, id uint) {
	t.Helper()
	count, err := repo.redis.Exists(context.Background(), sessionCacheKeys(id)...).Result()
	if err != nil || count != 0 {
		t.Fatalf("remaining cache keys = %d, err = %v", count, err)
	}
}

func TestSessionCacheLifecycle(t *testing.T) {
	repo, account := sessionCacheFixture(t)
	ctx := context.Background()
	a, b, c, d := auth.HashToken("a"), auth.HashToken("b"), auth.HashToken("c"), auth.HashToken("d")
	refresh := auth.HashRefreshToken("refresh")
	if err := repo.SaveSession(ctx, account.ID, a, refresh, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	expectAccessCache(t, repo, account.ID, a)
	var queries atomic.Int64
	if err := repo.db.Callback().Query().Before("gorm:query").Register("test:count_queries", func(*gorm.DB) { queries.Add(1) }); err != nil {
		t.Fatal(err)
	}
	expectActive(t, repo, account.ID, a, true)
	expectActive(t, repo, account.ID, b, false)
	if queries.Load() != 0 {
		t.Fatal("cache hits unexpectedly queried MySQL")
	}
	if err := repo.UpdateAccessTokenForRefresh(ctx, account.ID, "wrong-refresh", b); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("invalid refresh error: %v", err)
	}
	expectAccessCache(t, repo, account.ID, a)
	if err := repo.UpdateAccessTokenForRefresh(ctx, account.ID, refresh, b); err != nil {
		t.Fatal(err)
	}
	expectAccessCache(t, repo, account.ID, b)
	expectActive(t, repo, account.ID, a, false)
	if err := repo.RenameWithToken(ctx, account.ID, b, account.Username+"_renamed", c); err != nil {
		t.Fatal(err)
	}
	expectAccessCache(t, repo, account.ID, c)
	expectActive(t, repo, account.ID, b, false)
	if err := repo.ClearSession(ctx, account.ID, c); err != nil {
		t.Fatal(err)
	}
	expectNoAccessCache(t, repo, account.ID)
	expectActive(t, repo, account.ID, c, false)
	if err := repo.SaveSession(ctx, account.ID, d, refresh, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.ClearSession(ctx, account.ID, c); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale logout error: %v", err)
	}
	expectAccessCache(t, repo, account.ID, d)
	if err := repo.ChangePassword(ctx, account.ID, "wrong-password-hash", "new-password-hash"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale password update error: %v", err)
	}
	expectAccessCache(t, repo, account.ID, d)
	if err := repo.ChangePassword(ctx, account.ID, account.Password, "new-password-hash"); err != nil {
		t.Fatal(err)
	}
	expectNoAccessCache(t, repo, account.ID)
	expectActive(t, repo, account.ID, d, false)
	current, err := repo.FindByID(ctx, account.ID)
	if err != nil || current.Password != "new-password-hash" || current.RefreshTokenHash != nil || current.AccessTokenHash != nil {
		t.Fatalf("password change did not revoke the database session: %+v, %v", current, err)
	}
	if err := repo.UpdateAccessTokenForRefresh(ctx, account.ID, refresh, a); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("revoked refresh error: %v", err)
	}
}

func TestSessionCacheMissAndAbandonedMarkerRepair(t *testing.T) {
	repo, account := sessionCacheFixture(t)
	ctx := context.Background()
	hash := auth.HashToken("current")
	if err := repo.SaveSession(ctx, account.ID, hash, "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	keys := sessionCacheKeys(account.ID)
	if err := repo.redis.Del(ctx, keys...).Err(); err != nil {
		t.Fatal(err)
	}
	expectActive(t, repo, account.ID, hash, true)
	expectAccessCache(t, repo, account.ID, hash)
	// 模拟旧版本遗留值，以及数据库事务已结束但发布未完成的标记。
	for _, state := range []string{"", "updating:abandoned"} {
		if err := repo.redis.Del(ctx, keys...).Err(); err != nil {
			t.Fatal(err)
		}
		if err := repo.redis.Set(ctx, keys[0], "old-hash", time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		if state != "" {
			if err := repo.redis.Set(ctx, keys[1], state, 0).Err(); err != nil {
				t.Fatal(err)
			}
		}
		expectActive(t, repo, account.ID, "old-hash", false)
		expectAccessCache(t, repo, account.ID, hash)
	}
}

func cloneSessionClient(t *testing.T, repo *AccountRepository, hook redis.Hook) *redis.Client {
	t.Helper()
	options := *repo.redis.Options()
	client := redis.NewClient(&options)
	if hook != nil {
		client.AddHook(hook)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

type sessionCommandHook struct {
	process func(context.Context, redis.Cmder, redis.ProcessHook) error
}

func (h sessionCommandHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h sessionCommandHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h sessionCommandHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { return h.process(ctx, cmd, next) }
}

func isSessionScript(cmd redis.Cmder, script *redis.Script) bool {
	args := cmd.Args()
	if len(args) < 2 || (cmd.Name() != "evalsha" && cmd.Name() != "eval") {
		return false
	}
	return fmt.Sprint(args[1]) == script.Hash() || (cmd.Name() == "eval" && redis.NewScript(fmt.Sprint(args[1])).Hash() == script.Hash())
}

func TestSessionCacheRedisUnavailable(t *testing.T) {
	repo, account := sessionCacheFixture(t)
	ctx := context.Background()
	hash := auth.HashToken("original")
	if err := repo.SaveSession(ctx, account.ID, hash, "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1, DialTimeout: 100 * time.Millisecond, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	degraded := NewAccountRepository(repo.db, client, repo.accessTokenTTL)
	expectActive(t, degraded, account.ID, hash, true)
	expectActive(t, degraded, account.ID, "wrong", false)
	if err := degraded.SaveSession(ctx, account.ID, "replacement", "new-refresh", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("session was changed without confirming cache invalidation")
	}
	if err := degraded.ClearSession(ctx, account.ID, hash); err == nil {
		t.Fatal("logout should report unavailable cache invalidation")
	}
	current, err := repo.FindByID(ctx, account.ID)
	if err != nil || current.AccessTokenHash == nil || *current.AccessTokenHash != hash {
		t.Fatal("failed invalidation changed the database session")
	}
	expectAccessCache(t, repo, account.ID, hash)
}

func TestSessionCachePublishFailureFallsBackWithoutRevivingOldToken(t *testing.T) {
	repo, account := sessionCacheFixture(t)
	ctx := context.Background()
	a, b := auth.HashToken("a"), auth.HashToken("b")
	if err := repo.SaveSession(ctx, account.ID, a, "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	client := cloneSessionClient(t, repo, sessionCommandHook{process: func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
		if isSessionScript(cmd, publishSessionScript) {
			return errors.New("simulated publish failure")
		}
		return next(ctx, cmd)
	}})
	degraded := NewAccountRepository(repo.db, client, repo.accessTokenTTL)
	if err := degraded.UpdateAccessTokenForRefresh(ctx, account.ID, "refresh", b); err != nil {
		t.Fatal(err)
	}
	state, err := repo.redis.Get(ctx, sessionCacheKeys(account.ID)[1]).Result()
	if err != nil || !strings.HasPrefix(state, "updating:") {
		t.Fatalf("missing protective marker: %q, %v", state, err)
	}
	expectActive(t, repo, account.ID, a, false)
	expectAccessCache(t, repo, account.ID, b)
	if err := degraded.ClearSession(ctx, account.ID, b); err != nil {
		t.Fatal(err)
	}
	expectActive(t, repo, account.ID, b, false)
	expectNoAccessCache(t, repo, account.ID)
}

func TestSessionCacheAmbiguousInvalidationRollsBackAndRepairs(t *testing.T) {
	repo, account := sessionCacheFixture(t)
	ctx := context.Background()
	hash := auth.HashToken("original")
	if err := repo.SaveSession(ctx, account.ID, hash, "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	client := cloneSessionClient(t, repo, sessionCommandHook{process: func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
		err := next(ctx, cmd)
		if err == nil && isSessionScript(cmd, beginSessionUpdateScript) {
			return errors.New("invalidation applied but reply lost")
		}
		return err
	}})
	degraded := NewAccountRepository(repo.db, client, repo.accessTokenTTL)
	if err := degraded.SaveSession(ctx, account.ID, "new", "new-refresh", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected failed invalidation acknowledgement")
	}
	expectActive(t, repo, account.ID, "new", false)
	expectAccessCache(t, repo, account.ID, hash)
}

func blockingSessionPublisher(t *testing.T, repo *AccountRepository) (*AccountRepository, <-chan struct{}, func()) {
	t.Helper()
	reached := make(chan struct{})
	release := make(chan struct{})
	var reachedOnce, releaseOnce sync.Once
	client := cloneSessionClient(t, repo, sessionCommandHook{process: func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
		if isSessionScript(cmd, publishSessionScript) {
			reachedOnce.Do(func() { close(reached) })
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return next(ctx, cmd)
	}})
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	return NewAccountRepository(repo.db, client, repo.accessTokenTTL), reached, unblock
}

func TestSessionCacheDelayedLogoutCannotDeleteNewLogin(t *testing.T) {
	repo, account := sessionCacheFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := auth.HashToken("a"), auth.HashToken("b")
	if err := repo.SaveSession(ctx, account.ID, a, "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	blocked, reached, unblock := blockingSessionPublisher(t, repo)
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); result <- blocked.ClearSession(ctx, account.ID, a) }()
	t.Cleanup(func() { unblock(); <-finished })
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("logout did not reach the cache publish barrier")
	}
	if err := repo.SaveSession(ctx, account.ID, b, "new-refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	expectAccessCache(t, repo, account.ID, b)
	expectActive(t, repo, account.ID, a, false)
}

func TestSessionCacheRefillCannotRacePastLogout(t *testing.T) {
	repo, account := sessionCacheFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hash := auth.HashToken("active")
	if err := repo.SaveSession(ctx, account.ID, hash, "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.redis.Del(ctx, sessionCacheKeys(account.ID)...).Err(); err != nil {
		t.Fatal(err)
	}
	blocked, reached, unblock := blockingSessionPublisher(t, repo)
	readDone, writeDone := make(chan struct{}), make(chan struct{})
	readResult, writeResult := make(chan error, 1), make(chan error, 1)
	go func() {
		defer close(readDone)
		active, err := blocked.IsAccessTokenActive(ctx, account.ID, hash)
		if err == nil && !active {
			err = errors.New("in-flight read lost the active session")
		}
		readResult <- err
	}()
	t.Cleanup(func() { unblock(); <-readDone })
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("read did not reach the refill barrier")
	}
	go func() { defer close(writeDone); writeResult <- repo.ClearSession(ctx, account.ID, hash) }()
	t.Cleanup(func() { unblock(); <-writeDone })
	select {
	case err := <-writeResult:
		t.Fatalf("logout bypassed the refill row lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	if err := <-readResult; err != nil {
		t.Fatal(err)
	}
	if err := <-writeResult; err != nil {
		t.Fatal(err)
	}
	expectNoAccessCache(t, repo, account.ID)
	expectActive(t, repo, account.ID, hash, false)
}
