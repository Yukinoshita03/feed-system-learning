# 账号接口 API 自动化测试

测试使用 pytest、Requests 和 Allure，覆盖改密、改名、按 ID/用户名查询、头像上传和个人资料更新接口。`test_account_cache.py` 进一步通过真实 Redis 检查缓存值、TTL、刷新替换、退出/改密删除、重新登录、回填和旧缓存拒绝。

## 启动隔离的测试后端

先准备一个专用 MySQL 数据库，然后在 `backend/` 目录启动服务。下面示例使用独立端口和头像目录，避免影响默认的 8081 服务和已有头像文件：

```bash
export MYSQL_DSN='root@tcp(127.0.0.1:3306)/feed_system_pytest?charset=utf8mb4&parseTime=True&loc=Local'
export JWT_SECRET='local-only-pytest-signing-secret'
export REDIS_ADDR='127.0.0.1:6379'
export REDIS_PASSWORD=''
export REDIS_DB='15'
export ACCESS_TOKEN_TTL='15m'
export HTTP_ADDR=':8082'
export UPLOAD_DIR='/tmp/feed-system-pytest-uploads'
export PUBLIC_BASE_URL='http://127.0.0.1:8082'
go run ./cmd
```

`go run ./cmd` 会自动迁移账号表。运行前可用 MySQL 创建空的测试数据库：

```sql
CREATE DATABASE feed_system_pytest CHARACTER SET utf8mb4;
```

## 安装和运行

在项目根目录执行：

```bash
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r tests/requirements.txt
FEED_API_BASE_URL='http://127.0.0.1:8082' \
FEED_TEST_REDIS_ADDR="$REDIS_ADDR" \
FEED_TEST_REDIS_PASSWORD="$REDIS_PASSWORD" \
FEED_TEST_REDIS_DB="$REDIS_DB" \
  python -m pytest tests/api --alluredir=reports/allure-results
```

命令退出后，可用 Allure CLI 打开报告：

```bash
allure serve reports/allure-results
```

缓存用例使用 `redis-cli`（Redis 自带客户端），无需增加 Python Redis 依赖。未设置 `FEED_TEST_REDIS_ADDR` 或 `FEED_TEST_REDIS_DB` 时，这些用例会明确跳过；要验证完整缓存行为，须将它们设置为被测后端使用的测试 Redis 地址和逻辑数据库。请使用至少一分钟的 access token 有效期运行这组完整回归。

用例会创建测试账号，并上传一张小型 PNG。请在专用 MySQL 测试数据库、Redis 逻辑数据库和测试头像目录中运行；清理时删除本轮测试账号对应的 `auth:access:<账号ID>`、`auth:access:state:<账号ID>`，以及测试 MySQL 数据库和 `UPLOAD_DIR`。不要清空共享 Redis。

## 登录态缓存集成测试

`backend/internal/account/session_cache_integration_test.go` 连接真实 MySQL 和 Redis，检查缓存命中不查询 MySQL、刷新/改名更新、退出/改密撤销、缓存回填、Redis 故障与并发请求。未显式配置连接信息时会跳过这些集成测试；跳过不代表通过。

准备好上面的专用数据库后，在 `backend/` 目录运行：

```bash
FEED_TEST_MYSQL_DSN="$MYSQL_DSN" \
FEED_TEST_REDIS_ADDR="$REDIS_ADDR" \
FEED_TEST_REDIS_PASSWORD="$REDIS_PASSWORD" \
FEED_TEST_REDIS_DB="$REDIS_DB" \
  go test -race ./internal/account -run TestSessionCache -v
```

Go 集成测试会自动迁移专用数据库中的账号表，并在结束时清理本测试创建的账号与 Redis key。
