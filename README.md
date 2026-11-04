# Feed System 学习项目

## 目录

- `frontend/`：原项目的 Vue 前端。
- `backend/go.mod`：Go 模块定义。
- `backend/cmd/`：服务启动入口。
- `backend/internal/account/`：账号模块的 Handler、Service 和 Repo。
- `backend/internal/auth/`：JWT 访问 token 的生成。

## 当前进度

后端已实现账号注册、密码验证、登录时签发 JWT，以及使用 `Authorization: Bearer <token>` 访问受保护接口 `GET /account/me`。登录返回 15 分钟有效的访问 token 和 7 天有效的 refresh token；`POST /account/refresh` 使用 refresh token 换取访问 token。

数据库保存访问 token 的哈希、refresh token 的哈希及其过期时间。每个账号保留一套当前凭证，重新登录会替换旧凭证；刷新后旧访问 token 失效。`POST /account/logout` 清除当前凭证，退出后旧的访问 token 和 refresh token 均不可再使用。当前登录状态每次从 MySQL 检查，尚未接入 Redis。

重启后 `AutoMigrate` 会为已有账号表增加缺失的凭证列；已有用户需要重新登录，以建立当前登录状态。

后端启动需要 `MYSQL_DSN`；可通过 `JWT_SECRET` 配置签名密钥。如果没有设置，程序会生成临时密钥，服务重启后此前的 token 会失效。前端开发代理指向本机 8081 端口；其他原项目接口仍待逐步实现。
