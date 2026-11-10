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

// Migrate 在多个测试包并行使用同一个测试库时也能工作。
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
