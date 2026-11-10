// Package dbtest 保存集成测试共用的数据库准备逻辑。
// 它只在测试代码里被引用，不会进入服务端的构建产物。
package dbtest

import (
	"errors"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// Migrate 建表并处理并发冲突。
//
// 注意：每个 fixture 必须把自己断言会触及的**所有**表都列进来，不能只列
// 主要那几张。常见漏项是被删视频时连带清理的从表（likes/comments/favorites/
// video_views）：业务代码往 DeleteVideo 里加一张表，忘记同步 fixture，就会在
// 跑测试时报 "Table ... doesn't exist"。
//
// go test ./... 会并行运行不同包的测试二进制，它们会对同一批表调用 AutoMigrate。
// GORM 的 AutoMigrate 是"先 HasTable 再 CREATE"，并发时后到的一方会拿到
// MySQL 1050（table already exists）。这里对这种冲突退避重试，而不是直接判失败。
func Migrate(t *testing.T, db *gorm.DB, models ...any) {
	t.Helper()
	MigrateFunc(t, func() error { return db.AutoMigrate(models...) })
}

// MigrateFunc 对任意建表函数做同样的退避重试。
func MigrateFunc(t *testing.T, migrate func() error) {
	t.Helper()
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = migrate(); err == nil {
			return
		}
		if !isTableExists(err) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("并发建表重试仍然失败：%v", err)
}

func isTableExists(err error) bool {
	var mysqlErr *driver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1050
}
