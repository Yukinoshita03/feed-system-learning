# Feed System 学习项目

## 目录

- `frontend/`：原项目的 Vue 前端。
- `backend/go.mod`：Go 模块定义。
- `backend/cmd/`：服务启动入口。
- `backend/internal/account/`：账号模块的 Handler、Service 和 Repo。
- `backend/internal/auth/`：JWT 访问 token 的生成。

## 当前进度

账号模块已实现注册、登录、刷新 token、获取当前账号、退出登录、修改密码、修改用户名、按 ID/用户名查询公开资料、上传头像和更新个人资料。需要登录的路由通过 `Authorization: Bearer <token>` 鉴权。

登录默认返回 15 分钟有效的访问 token 和 7 天有效的 refresh token。访问 token 有效期可通过 `ACCESS_TOKEN_TTL` 配置，同一配置注入 JWT 签发器和账号 Repo，用于 JWT 的有效期与登录缓存的 TTL。数据库保存凭证哈希及 refresh token 过期时间；每个账号保留一套当前凭证，重新登录会替换旧凭证，刷新后旧访问 token 失效。退出登录和修改密码都会撤销当前凭证；修改用户名会返回新的访问 token。

头像文件保存在 `.run/uploads/avatars/<account_id>/`，并通过 `/static/...` 提供访问。账号表通过 `AutoMigrate` 增加头像地址和个人简介列。若服务位于反向代理之后，可将 `PUBLIC_BASE_URL` 设置为浏览器能访问的后端 origin，例如 `https://api.example.com`。

登录态校验优先读取 Redis；缓存未命中、正在更新或 Redis 查询失败时，回源 MySQL 并尝试回填。登录、刷新和改名会更新访问凭证缓存，退出与修改密码会清除它。refresh token 仍由 MySQL 校验。后端启动需要 `MYSQL_DSN`；可通过 `JWT_SECRET` 配置签名密钥。如果没有设置，程序会生成临时密钥，服务重启后此前的 access token 会失效。前端开发代理指向本机 8081 端口；Feed 业务的其他接口仍待逐步实现。

## 登录态缓存

| Repo 方法 | 缓存行为 |
| --- | --- |
| `SaveSession` | 登录后写入新 access token 哈希和注入的 TTL |
| `UpdateAccessTokenForRefresh` | 刷新后替换 access token 哈希，并重新设置 TTL |
| `RenameWithToken` | 改名后替换 access token 哈希，并重新设置 TTL |
| `ClearSession` | 验证是当前会话后撤销数据库凭证并清除缓存 |
| `ChangePassword` | 验证旧密码哈希未变化后更新密码、撤销凭证并清除缓存 |
| `IsAccessTokenActive` | 优先读取可信缓存，必要时回源并回填 |

每个账号使用两个 key：`auth:access:<账号ID>` 保存访问凭证哈希，`auth:access:state:<账号ID>` 保存 `ready` 或带随机标识的 `updating:...` 状态。鉴权通过一次 `MGET` 同时读取它们，仅信任 `ready` 的值；旧版本没有状态 key 的缓存会重新查询数据库。

更新登录态时，先在 MySQL 事务中锁定账号行，再用 Lua 标记更新中并删除旧缓存，然后更新数据库并提交。提交后，Lua 仅在标记仍属于本次请求时发布新缓存或清除 key，避免晚到的旧退出请求删除新登录态。缓存回填也持有相同账号行锁，避免旧查询在退出后把旧凭证重新写回。公共实现位于 `backend/internal/account/session_cache.go`，各 Repo 方法只声明自己的字段更新与校验条件。

运行中的 Redis 查询失败时，鉴权使用 MySQL 结果。凭证变更若无法确认旧缓存已失效，会返回错误并回滚数据库事务；数据库提交后若缓存发布失败，则保留更新中状态，让后续鉴权回源并修复。该流程依赖所有凭证修改统一经过 Repo，不构成 Redis 与 MySQL 之间的分布式事务。实现依据见 [GORM 行锁](https://gorm.io/docs/advanced_query.html#Locking) 和 [Redis Lua 原子执行](https://redis.io/docs/latest/develop/programmability/eval-intro/)。

## Redis 配置

启动后端前，在同一个终端设置环境变量：

```bash
export REDIS_ADDR='127.0.0.1:6379'
export REDIS_PASSWORD=''
export REDIS_DB='0'
export ACCESS_TOKEN_TTL='15m'
# Redis 配置了密码时，将 REDIS_PASSWORD 设置为实际密码。
```

`REDIS_ADDR` 未设置时默认使用 `127.0.0.1:6379`，`REDIS_PASSWORD` 默认空字符串，`REDIS_DB` 默认 `0`，并校验其为非负整数。

`ACCESS_TOKEN_TTL` 默认 `15m`，支持 `time.ParseDuration` 格式，例如 `30m`、`1h`，且至少为 `1s`。有效期只在启动入口确定一次，再通过构造函数传给 JWT 签发器和账号 Repo；refresh token 的 7 天有效期单独处理。

保留已有的 `MYSQL_DSN` 设置，在 `backend/` 目录运行 `go run ./cmd`。启动时会执行一次带 3 秒超时的 Redis `PING`；成功后打印 `Redis 连接成功：127.0.0.1:6379，DB=0`，连接失败则报告错误并退出。`NewRedis` 仍只负责创建客户端，启动入口负责检查连接和管理客户端生命周期。

## 账号接口自动化测试

pytest + Requests + Allure 用例位于 `tests/api/`。请使用专用测试数据库启动后端，并显式设置 `FEED_API_BASE_URL`；测试会注册账号，不要指向包含真实学习数据的数据库。测试依赖和命令见 [`tests/README.md`](tests/README.md)。
