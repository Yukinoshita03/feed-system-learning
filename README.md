# Feed System 学习项目

## 目录

- `frontend/`：原项目的 Vue 前端。
- `backend/go.mod`：Go 模块定义。
- `backend/cmd/`：服务启动入口。
- `backend/internal/account/`：账号模块的 Handler、Service 和 Repo。
- `backend/internal/auth/`：JWT 访问 token 的生成。

## 当前进度

账号模块已实现注册、登录、刷新 token、获取当前账号、退出登录、修改密码、修改用户名、按 ID/用户名查询公开资料、上传头像和更新个人资料。需要登录的路由通过 `Authorization: Bearer <token>` 鉴权。

登录返回 15 分钟有效的访问 token 和 7 天有效的 refresh token。数据库保存凭证哈希及 refresh token 过期时间；每个账号保留一套当前凭证，重新登录会替换旧凭证，刷新后旧访问 token 失效。退出登录和修改密码都会撤销当前凭证；修改用户名会返回新的访问 token。

头像文件保存在 `.run/uploads/avatars/<account_id>/`，并通过 `/static/...` 提供访问。账号表通过 `AutoMigrate` 增加头像地址和个人简介列。若服务位于反向代理之后，可将 `PUBLIC_BASE_URL` 设置为浏览器能访问的后端 origin，例如 `https://api.example.com`。

当前登录状态每次从 MySQL 检查，尚未接入 Redis。后端启动需要 `MYSQL_DSN`；可通过 `JWT_SECRET` 配置签名密钥。如果没有设置，程序会生成临时密钥，服务重启后此前的 access token 会失效。前端开发代理指向本机 8081 端口；Feed 业务的其他接口仍待逐步实现。

## 账号接口自动化测试

pytest + Requests + Allure 用例位于 `tests/api/`。请使用专用测试数据库启动后端，并显式设置 `FEED_API_BASE_URL`；测试会注册账号，不要指向包含真实学习数据的数据库。测试依赖和命令见 [`tests/README.md`](tests/README.md)。
