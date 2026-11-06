# Feed System 学习项目

启动、环境变量、Selenium 页面测试和 Allure 报告命令见 **[运行与测试指令](docs/运行与测试指令.md)**；按文档中的终端顺序执行即可。

## 目录

- `frontend/`：原项目的 Vue 前端。
- `backend/go.mod`：Go 模块定义。
- `backend/cmd/`：服务启动入口。
- `backend/internal/account/`：账号模块的 Handler、Service 和 Repo。
- `backend/internal/auth/`：JWT 访问 token 的生成。
- `backend/internal/video/`：视频资料的 Repo、Service、Handler。
- `backend/internal/feed/`：最新视频流的 Repo、Service、Handler 和请求、响应结构。
- `backend/internal/cache/`：视频与 Feed 共享的 Redis 缓存、版本校验和 TTL 配置。

## 当前进度

账号模块已实现注册、登录、刷新 token、获取当前账号、退出登录、修改密码、修改用户名、按 ID/用户名查询公开资料、上传头像和更新个人资料。需要登录的路由通过 `Authorization: Bearer <token>` 鉴权。

登录默认返回 15 分钟有效的访问 token 和 7 天有效的 refresh token。访问 token 有效期可通过 `ACCESS_TOKEN_TTL` 配置，同一配置注入 JWT 签发器和账号 Repo，用于 JWT 的有效期与登录缓存的 TTL。数据库保存凭证哈希及 refresh token 过期时间；每个账号保留一套当前凭证，重新登录会替换旧凭证，刷新后旧访问 token 失效。退出登录和修改密码都会撤销当前凭证；修改用户名会返回新的访问 token。

头像文件保存在 `.run/uploads/avatars/<account_id>/`，并通过 `/static/...` 提供访问。账号表通过 `AutoMigrate` 增加头像地址和个人简介列。若服务位于反向代理之后，可将 `PUBLIC_BASE_URL` 设置为浏览器能访问的后端 origin，例如 `https://api.example.com`。

登录态校验优先读取 Redis；缓存未命中、正在更新或 Redis 查询失败时，回源 MySQL 并尝试回填。登录、刷新和改名会更新访问凭证缓存，退出与修改密码会清除它。refresh token 仍由 MySQL 校验。后端启动需要 `MYSQL_DSN`；可通过 `JWT_SECRET` 配置签名密钥。如果没有设置，程序会生成临时密钥，服务重启后此前的 access token 会失效。前端开发代理指向本机 8081 端口；Feed 业务的其他接口仍待逐步实现。

## 视频资料 CRUD

`Video` 包含八个字段：`id`、`author_id`、`title`、`play_url`、`cover_url`、`likes_count`、`comments_count`、`create_time`。新建视频记录时由 GORM 自动填写发布时间，点赞数默认为 0；启动后自动迁移 `videos`、`likes` 表及索引。旧记录的真实发布时间无法自动还原。

| 接口（均为 POST） | 登录要求 | 请求内容 |
| --- | --- | --- |
| `/video/publish` | 需要 | `title`、`play_url`、`cover_url` |
| `/video/getDetail` | 公开 | `id` |
| `/video/listByAuthorID` | 公开 | `author_id`，返回最近 200 条记录，按 ID 倒序 |
| `/video/update` | 需要，且为作者 | `id`、完整的 `title`、`play_url`、`cover_url` |
| `/video/delete` | 需要，且为作者 | `id` |

新增和修改返回视频对象，删除返回提示消息。标题会去除首尾空白，长度为 1～255 个字符；媒体地址支持 HTTP/HTTPS 或 `/static/` 路径，最多 512 字节。作者 ID 来自登录中间件，客户端不能修改作者。非法参数返回 400、未登录返回 401、操作其他作者的视频返回 403、记录不存在返回 404。

资料 CRUD 接收文件地址，不检查文件是否存在。普通视频与封面上传、视频资料缓存已接入，分片上传和 MQ 事件后续实现。

## 普通视频上传

`POST /video/uploadVideo` 需要 Bearer token，Body 使用 `form-data`，字段名为 `file`。支持最大 200 MiB 的 MP4，检查扩展名和文件头；不在上传阶段执行转码或完整媒体解码校验。文件以随机名称保存到 `UPLOAD_DIR/videos/<账号ID>/`，默认目录为 `backend/.run/uploads/videos/<账号ID>/`（在 `backend/` 启动服务时）。

成功返回 `url` 和 `play_url` 两个字段，值相同，格式为 `http://127.0.0.1:8081/static/videos/3/<随机名>.mp4`。`PUBLIC_BASE_URL` 可覆盖返回地址的 origin。上传只保存文件，之后将 `play_url` 传给 `/video/publish` 创建视频记录。处理代码位于 `backend/internal/video/upload.go`，沿用已有 `/static` 静态文件路由。

## 封面上传

`POST /video/uploadCover` 需要 Bearer token，Body 使用 `form-data`，文件字段名为 `file`。支持 JPG/JPEG、PNG、WEBP，最大 10 MiB，同时检查扩展名和文件头，未执行完整图片解码。封面以随机名称保存到 `UPLOAD_DIR/covers/<账号ID>/`，通过 `/static/covers/<账号ID>/<随机名>` 访问。

响应返回相同的 `url`、`cover_url`，沿用 `PUBLIC_BASE_URL` 配置。封面上传只保存文件；将其 `cover_url` 与视频上传返回的 `play_url` 一起提交到 `/video/publish`，才会创建视频记录。代码位于 `backend/internal/video/cover_upload.go`。

发布页默认调用普通视频上传和封面上传，再提交发布。原有分片流程保留，可在接口完成后通过 `VITE_ENABLE_CHUNK_UPLOAD=true` 启用。前端开发代理可通过 `API_PROXY_TARGET` 指定测试后端，默认仍为 `http://127.0.0.1:8081`。Selenium 页面测试及运行说明见 `tests/ui/` 和 `tests/README.md`。

## 视频点赞

四个接口均为 POST，使用 `Authorization: Bearer <token>` 验证登录态。点赞人从 JWT 获取，请求中的 `account_id` 不参与身份判断。

| 接口 | 请求 | 返回 |
| --- | --- | --- |
| `/like/like` | `video_id` | `message`、`is_liked`、`likes_count` |
| `/like/unlike` | `video_id` | `message`、`is_liked`、`likes_count` |
| `/like/isLiked` | `video_id` | 当前账号的 `is_liked` |
| `/like/listMyLikedVideos` | `{}` | 当前账号赞过的视频数组，按点赞时间、点赞记录 ID 倒序，最多 200 条 |

`likes` 表保存 `id`、`video_id`、`account_id`、`created_at`，以 `(video_id, account_id)` 为联合唯一索引。点赞和取消在一个 MySQL 事务中先锁定视频行，再更新关系记录和视频计数。重复点赞、重复取消均成功返回当前状态，计数只在关系实际变化时加减；计数更新失败时，关系记录也会回滚。

成功操作后使视频详情缓存失效；两个公共列表只保存 ID，可继续复用。前端使用服务端返回的状态和计数更新红心，刷新后通过接口恢复。“我赞过的视频”直接查询当前账号的关系，不复用其他用户的状态。删除视频时按相同锁顺序，在事务中清理点赞记录和视频，避免并发点赞产生孤立记录。

未登录返回 401、非法参数返回 400、视频不存在返回 404、内部错误返回通用的 500 提示。账号页已接入点赞列表，首页和详情页均支持点赞、取消及刷新保持状态。

## 最新视频流

`FeedService.ListLatest` 已实现参数校验、批量补充作者公开信息和分页响应。每页默认 10 条，允许 1～50 条；通过多查询一条判断 `has_more`，返回 `video_list`、`next_time`、`next_id` 和 `has_more`。

第一页的 `latest_time`、`latest_id` 都为 0；后续同时传入上一页的 `next_time`、`next_id`。查询按 `create_time DESC, id DESC` 排序，使用时间和 ID 的组合条件，避免同一毫秒发布的视频在翻页时遗漏。时间字段使用毫秒时间戳。

旧视频缺少有效发布时间时，补充时间后才会进入这一时间分页列表；不会自动猜测旧记录的真实发布时间。作者不存在时保留作者 ID、显示“未知用户”。`likes_count` 来自公共视频资料，`is_liked` 按当前登录账号批量查询关系；游客的 `is_liked` 为 false，个人状态不存入公共缓存。

`POST /feed/listLatest` 允许游客访问；提供 token 时通过 OptionalJWT 检查签名、有效期及会话撤销状态，失效凭证返回 401。首页推荐流会同时保存和传递时间、ID 游标，滑动到列表末尾附近时加载下一页；保留全部滚动占位，只挂载当前及相邻视频的播放内容。

后端直连示例（通过 Vite 代理时路径为 `/api/feed/listLatest`）：

```bash
curl --fail http://127.0.0.1:8081/feed/listLatest \
  -H 'Content-Type: application/json' \
  -d '{"limit":10,"latest_time":0,"latest_id":0}'
```

参数或 JSON 格式错误返回 400；内部错误返回通用的 500 提示，并由 Gin 记录。API、真实 MySQL 和 Selenium 回归的运行方式见 [测试说明](tests/README.md)。点赞榜、关注流等其他 Feed 接口仍待实现。

## 视频与 Feed 缓存

复用启动时创建的 Redis 客户端；配置由 `cache.LoadConfig` 读取一次，通过 `cache.New` 和 Router 注入 Video、Feed Repo。

| 缓存 | Key | 默认有效期 | 内容 |
| --- | --- | --- | --- |
| 视频详情 | `video:detail:<视频ID>` | 5 分钟 | 公共视频资料 JSON |
| 作者作品 | `video:author:<作者ID>` | 1 分钟 | 最近 200 条作品的有序 ID 列表 |
| 首页第一页 | `feed:latest:first:10` | 30 秒 | 默认每页 10 条所需的前 11 个视频 ID，用额外一条判断 `has_more` |

详情支持批量读取 Redis、批量回源 MySQL，并按传入 ID 顺序返回。两个列表只缓存 ID，读取时通过详情缓存补充资料；Feed 作者用户名继续批量查 MySQL，用户相关的点赞状态不放入公共缓存。其他页大小和后续分页直接查 MySQL。视频、图片文件仍通过 `/static` 提供，不存入 Redis。

发布成功后清除新视频详情、作者作品列表和首页缓存；修改标题或媒体地址后清除详情；删除成功后清除详情及两个列表。更新、删除仍使用 MySQL 的作者条件校验。Redis 读取失败、缓存内容损坏或过期时回源 MySQL，写回缓存失败不影响数据库查询结果。

每个数据 key 配有 `cache:version:<数据key>`。缓存读取时先取得随机版本，回填时通过 Lua 比较版本；成功失效会删除数据并替换版本，阻止此前的慢查询把旧结果写回。版本本身也有 TTL；过期后使用新随机版本，旧查询不能重新获得回填资格。

这是业务数据的最终一致性缓存：数据库写入成功后，使用独立短超时尝试失效；失效失败会记录日志，不回滚已经提交的数据库，已有旧值可能持续到缓存过期。版本校验不构成跨 MySQL、Redis 的强一致事务。登录态继续使用下方单独的撤销规则。

可在启动后端的终端覆盖默认配置，支持 `time.ParseDuration` 格式，最小为 1ms：

```bash
export VIDEO_CACHE_TTL='5m'
export AUTHOR_VIDEO_CACHE_TTL='1m'
export FEED_CACHE_TTL='30s'
export CACHE_TIMEOUT='200ms'
```

真实 MySQL、Redis 缓存命中、失效、并发回填与故障回归见 [测试说明](tests/README.md)。

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

## 社交业务 CRUD

已接入评论发布、游标分页查询和本人删除，视频删除会清理关联评论；评论数与记录在同一事务更新，并清除详情缓存。接口与参数见 [CRUD 接口说明](docs/CRUD接口说明.md)。
