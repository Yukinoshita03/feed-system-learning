# API 与 Selenium 页面测试

完整的首次安装、日常启动和独立测试环境命令见 [运行与测试指令](../docs/运行与测试指令.md)。下文示例使用独立的测试 Redis 端口 `6380`，先在另一个终端执行 `redis-server --bind 127.0.0.1 --port 6380 --save '' --appendonly no`，并保持其运行。

测试使用 pytest、Requests 和 Allure，覆盖改密、改名、按 ID/用户名查询、头像上传和个人资料更新接口。`test_account_cache.py` 进一步通过真实 Redis 检查缓存值、TTL、刷新替换、退出/改密删除、重新登录、回填和旧缓存拒绝。

`test_video_crud.py` 覆盖视频资料的完整增删改查、重复修改、作者权限、非法参数，以及作者列表过滤和排序。这组用例只写入文件地址，不上传视频文件。

`test_video_upload.py` 使用 `tests/fixtures/clip.mp4` 验证真实文件上传、原始字节一致性、Range 读取、随机文件名、上传与发布分离、登录校验、非法文件和超过 200 MiB 的实际上传拒绝。大文件用例流式生成内容，会临时使用约 200 MiB 磁盘；测试执行无需安装 FFmpeg。

`test_cover_upload.py` 使用 `tests/fixtures/cover.jpg`、`cover.png` 和 `cover.webp` 检查支持的图片格式、同名文件隔离、原始字节一致性、账号目录、非法文件、10 MiB 限制、登录校验，以及“视频上传 → 封面上传 → 发布 → 查询”的完整链路。图片素材是 16×16 纯色测试图片，运行测试无需生成素材的工具。

`test_feed_latest.py` 验证游客读取最新视频、默认页大小、时间和 ID 双游标分页、作者公开信息、删除后列表更新、空页和非法请求。视频 CRUD 回归同时检查更新资料后发布时间保持不变。

`test_video_cache.py` 通过真实 Redis 检查详情、作者列表和首页缓存的值与 TTL，更新/删除/发布后的失效、空作品列表更新、缓存过期/损坏后的修复，以及改名后首页作者信息仍为最新值。未提供 Redis 测试地址和逻辑库时会跳过这些缓存检查。

`test_likes.py` 覆盖四个点赞接口的登录要求、非法参数、重复请求幂等、计数、账号隔离、个人 Feed 状态、点赞列表顺序、删除视频后的清理、详情缓存失效、并发 HTTP 请求及撤销凭证校验。

## 启动隔离的测试后端

先准备一个专用 MySQL 数据库，然后在 `backend/` 目录启动服务。下面示例使用独立端口和头像目录，避免影响默认的 8081 服务和已有头像文件：

```bash
export MYSQL_DSN='root@tcp(127.0.0.1:3306)/feed_system_pytest?charset=utf8mb4&parseTime=True&loc=Local'
export JWT_SECRET='local-only-pytest-signing-secret'
export REDIS_ADDR='127.0.0.1:6380'
export REDIS_PASSWORD=''
export REDIS_DB='0'
export ACCESS_TOKEN_TTL='15m'
export HTTP_ADDR=':8082'
export UPLOAD_DIR='/tmp/feed-system-pytest-uploads'
export PUBLIC_BASE_URL='http://127.0.0.1:8082'
go run ./cmd
```

`go run ./cmd` 会自动迁移账号表和视频表。运行前可用 MySQL 创建空的测试数据库：

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
FEED_TEST_REDIS_ADDR='127.0.0.1:6380' \
FEED_TEST_REDIS_PASSWORD='' \
FEED_TEST_REDIS_DB='0' \
  python -m pytest tests/api --alluredir=reports/allure-results
```

命令退出后，可用 Allure CLI 打开报告：

```bash
allure serve reports/allure-results
```

缓存用例使用 `redis-cli`（Redis 自带客户端），无需增加 Python Redis 依赖。未设置 `FEED_TEST_REDIS_ADDR` 或 `FEED_TEST_REDIS_DB` 时，这些用例会明确跳过；要验证完整缓存行为，须将它们设置为被测后端使用的测试 Redis 地址和逻辑数据库。请使用至少一分钟的 access token 有效期运行这组完整回归。

用例会创建测试账号、视频资料并上传 PNG 和 MP4。请在专用 MySQL 测试数据库、Redis 逻辑数据库和测试上传目录中运行；清理时删除本轮测试账号对应的 `auth:access:<账号ID>`、`auth:access:state:<账号ID>`，以及测试 MySQL 数据库和 `UPLOAD_DIR`。不要清空共享 Redis。

## 登录态缓存集成测试

`backend/internal/account/session_cache_integration_test.go` 连接真实 MySQL 和 Redis，检查缓存命中不查询 MySQL、刷新/改名更新、退出/改密撤销、缓存回填、Redis 故障与并发请求。未显式配置连接信息时会跳过这些集成测试；跳过不代表通过。

准备好上面的专用数据库后，在 `backend/` 目录运行：

```bash
FEED_TEST_MYSQL_DSN='root@tcp(127.0.0.1:3306)/feed_system_pytest?charset=utf8mb4&parseTime=True&loc=Local' \
FEED_TEST_REDIS_ADDR='127.0.0.1:6380' \
FEED_TEST_REDIS_PASSWORD='' \
FEED_TEST_REDIS_DB='0' \
  go test -race ./internal/account -run TestSessionCache -v
```

Go 集成测试会自动迁移专用数据库中的账号表，并在结束时清理本测试创建的账号与 Redis key。

## Feed Service 集成测试

`backend/internal/feed/service_integration_test.go` 使用真实 MySQL，验证同一毫秒视频跨页无重复、无遗漏，默认页大小、空页、整页结束、作者公开信息、缺少发布时间的旧记录和请求取消的错误传播。测试只需专用 MySQL 数据库，无需启动 HTTP 服务或 Redis。

这组测试要求专用的空数据库，与 API 回归库分开。首次创建后执行：

```bash
mysql -h 127.0.0.1 -P 3306 -u root -e 'CREATE DATABASE IF NOT EXISTS feed_service_pytest CHARACTER SET utf8mb4;'
cd backend
FEED_TEST_MYSQL_DSN='root@tcp(127.0.0.1:3306)/feed_service_pytest?charset=utf8mb4&parseTime=True&loc=Local' \
  go test ./internal/feed -count=1 -v
```

集成测试会迁移账号表和视频表，测试数据在事务结束时回滚；未配置 `FEED_TEST_MYSQL_DSN` 时会跳过。`service_test.go` 中的非法参数检查不依赖数据库。

## 视频与 Feed 缓存并发测试

`backend/internal/feed/cache_integration_test.go` 连接真实 MySQL、Redis，检查预热后不查询视频表、注入的三种 TTL、生效后的缓存失效、过期和损坏后的修复、Redis 故障回源及写入，以及版本过期后拒绝旧回填。四组受控并发场景覆盖修改详情、删除详情、发布影响作者列表、发布影响首页；每组都会暂停已经读到旧数据库结果的请求，完成写入和失效后再释放旧请求。

先启动前文的独立 Redis 6380 进程，准备上面的空 `feed_service_pytest` 库。在 `backend/` 执行，使用 Redis DB 1 与 HTTP 测试的 DB 0 分开：

```bash
FEED_TEST_MYSQL_DSN='root@tcp(127.0.0.1:3306)/feed_service_pytest?charset=utf8mb4&parseTime=True&loc=Local' \
FEED_TEST_REDIS_ADDR='127.0.0.1:6380' \
FEED_TEST_REDIS_PASSWORD='' \
FEED_TEST_REDIS_DB='1' \
  go test -race -p 1 ./internal/cache ./internal/feed ./internal/video ./internal/middleware -count=1 -v
```

缓存测试使用已提交的测试记录验证跨请求效果，结束时删除自己创建的账号、视频及缓存 key；不清空整个 Redis。没有显式配置专用 MySQL、Redis 地址和 DB 时会跳过这些集成用例。缓存失效失败时数据库写入仍成功，最终依靠 TTL 收敛；测试不把它当作跨系统强一致性。

同一命令也会执行 `backend/internal/video/like_integration_test.go`：验证同一账号并发重复点赞/取消、多账号并发计数、计数更新失败的事务回滚、点赞列表排序、视频删除竞态和旧计数回填保护。测试通过独立 SQL 计数对比 `likes` 记录数和 `videos.likes_count`。由于这些包共用测试数据库，使用 `-p 1` 串行执行包；各测试内部仍会发起并发请求。点赞 SQL 测试需要显式 MySQL 连接，缓存相关检查还需要 Redis。

## Selenium 页面测试

`tests/ui/test_video_pages.py` 使用 Selenium 和真实 Chrome，覆盖未登录拦截、错误密码、页面注册/登录、发布必填字段、PNG/JPG/WEBP 封面发布、真实视频播放、非法封面提示和页面退出后的凭证撤销。测试通过页面输入账号和选择文件，不注入 token、不模拟接口响应；除专门测试页面注册的场景外，账号准备通过 API 完成。

`tests/ui/test_feed_page.py` 通过真实接口准备 13 条新视频，验证游客打开首页、真实视频播放、向下切换到第二页、时间和 ID 游标传递，以及最多挂载 3 个播放元素。截图和分页请求会附入 Allure。

`tests/ui/test_like_pages.py` 验证页面登录后首页点赞、刷新保持红心与计数、账号页查看点赞列表、详情页取消点赞，以及再次刷新和返回账号页后状态保持一致。

先按前文启动专用测试后端，再在另一个终端启动前端，例如：

```bash
cd frontend
API_PROXY_TARGET='http://127.0.0.1:8082' npm run dev -- --host 127.0.0.1 --port 5174 --strictPort
```

在项目根目录安装 `tests/requirements.txt` 中的依赖，然后执行：

```bash
FEED_API_BASE_URL='http://127.0.0.1:8082' \
FEED_WEB_BASE_URL='http://127.0.0.1:5174' \
  python -m pytest tests/ui -q --alluredir=reports/allure-results
```

默认使用无头 Chrome。设置 `FEED_UI_HEADLESS=0` 可显示浏览器窗口；可选 `FEED_UI_CHROME_BINARY`、`FEED_UI_CHROMEDRIVER` 指定本机浏览器和驱动路径，否则使用 Selenium 自带的驱动管理能力。每个用例使用独立浏览器会话，显式等待页面状态，失败截图、HTML 和 HTTP 错误响应会附入 Allure。

页面发布默认使用普通上传。前端 `VITE_ENABLE_CHUNK_UPLOAD=true` 开关保留原来的分片流程，需在分片接口完成后启用。当前关注、点赞榜等接口尚未实现，相关页面可能出现 404；UI 测试覆盖已实现的账号、视频发布/播放、最新视频流和点赞链路。

`test_comments.py` 验证评论发布、分页插入稳定性、计数和缓存失效、作者权限、非法正文、登录要求、并发发布和视频删除清理。
