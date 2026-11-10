# 社交业务 CRUD 接口

功能参考 [GCFeed](https://github.com/LeoninCS/GCFeed/tree/8cf995cfa03985c335594c453ea050df5c0f1b6a/apps/api/internal)，按本项目的 Gin、GORM、MySQL 和 `Repo → Service → Handler` 结构适配。

写接口的账号身份统一读取 Bearer token。传入其他 `author_id`、`account_id` 不会改变操作者。分页列表使用 `limit`（默认 20，最大 100）和 `before_id`；下一页传上次返回的 `next_id`，由 `has_more` 判断是否继续。

## 评论

| POST 路径 | 请求 JSON | 登录 | 响应 |
|---|---|---|---|
| `/comment/publish` | `{"video_id":1,"content":"一条评论"}` | 必须 | `comment`、`comments_count`，状态码 201 |
| `/comment/list` | `{"video_id":1,"limit":20,"before_id":0}` | 可不登录 | `comments`、`next_id`、`has_more` |
| `/comment/listAll` | 同上 | 可不登录 | 单页评论数组，兼容原页面 |
| `/comment/delete` | `{"comment_id":1}` | 必须 | 删除后的 `comments_count` |

正文去除首尾空白后要求 1～1000 个字符。只能删除自己的评论（越权 403）；视频或评论不存在返回 404。发布和删除在事务中锁定视频、修改评论并同步评论数，提交后使视频详情缓存失效。删除视频会同时清理评论。

## 收藏

以下接口均要求登录：

| POST 路径 | 请求 JSON | 响应 |
|---|---|---|
| `/favorite/favorite` | `{"video_id":1}` | `is_favorited`、`favorites_count` |
| `/favorite/unfavorite` | `{"video_id":1}` | 同上 |
| `/favorite/isFavorited` | `{"video_id":1}` | `is_favorited` |
| `/favorite/listMyFavorites` | `{"limit":20,"before_id":0}` | `video_list`、`next_id`、`has_more` |

列表按收藏记录 ID 倒序，游标是收藏关系 ID，视频字段 `id` 仍为视频 ID。重复操作不改变计数；列表只属于当前登录账号。计数修改后清除视频详情缓存；删除视频同步清理收藏关系。当前列表直接查 MySQL，不把个人收藏状态写进公共缓存。

## 关注

以下接口均要求登录：

| POST 路径 | 请求 JSON | 响应 |
|---|---|---|
| `/social/follow` | `{"vlogger_id":2}` | `is_following`、操作者的 `following_count`、目标的 `follower_count` |
| `/social/unfollow` | 同上 | 同上 |
| `/social/isFollowing` | 同上 | `is_following` |
| `/social/getAllFollowers` | `{"vlogger_id":2,"limit":20,"before_id":0}` | `followers`、`next_id`、`has_more`、`total` |
| `/social/getAllVloggers` | `{"follower_id":2,"limit":20,"before_id":0}` | `vloggers`、`next_id`、`has_more`、`total` |

列表不传目标 ID 时默认当前账号。列表只包含公开账号资料；游标使用关注关系 ID，用户的 `id` 仍是账号 ID。后端提供分页，现有页面逐页读取完整关系用于关注状态与列表展示。

不允许关注/取关自己，重复操作保持幂等。写入前按账号 ID 升序锁定双方账号，避免相互关注时锁顺序反转。关注关系使用唯一索引；关注数、粉丝数从关系表查询。关注流的视频查询在后续模块接入，本轮只完成关系 CRUD。

## 互动通知

以下接口均要求登录，接收人始终从 JWT 获取：

| POST 路径 | 请求 JSON | 响应 |
|---|---|---|
| `/notification/list` | `{"limit":20,"before_id":0,"unread_only":false}` | `messages`、`next_id`、`has_more` |
| `/notification/unreadCount` | `{}` | `unread_count` |
| `/notification/markRead` | `{"message_ids":[1,2]}` 或 `{"all":true}` | `updated_count`，实际从未读变为已读的条数 |
| `/notification/delete` | `{"message_id":1}` | 删除成功提示 |

点赞、收藏、评论和关注的新增操作在同一个 MySQL 事务中写入通知。通知记录失败会回滚业务记录和计数。唯一事件键由操作类型和业务记录 ID 组成；重复点赞/收藏/关注不重复发通知，取消后再次操作会生成新通知。自己的互动不通知自己。评论正文作为通知内容，其余类型使用固定提示。

通知没有对普通客户端开放“创建”接口。标记已读最多传 100 个 ID，空数组不会隐式标记全部；全量标记须显式传 `all=true`。操作其他人的 ID 不会改动其他人的通知；越权删除统一返回 404。

通知作为历史记录保留；取消点赞、取消收藏、取关、删除评论或视频时不会撤回已发出的通知，关联视频被删除后访问详情会返回 404。当前通知读取直接查询 MySQL。页面入口为 `/notifications`，提供刷新、加载更多、单条/全部已读和删除；原 `/messages` 私信页面仍是独立的待接入能力。

## 数据表与一致性

启动时 `AutoMigrate` 增加 `comments`、`favorites`、`follows`、`notifications`，并为视频增加 `comments_count`、`favorites_count`。视频删除先锁视频，再清理点赞、评论、收藏记录。所有相关写接口都使用一致的锁顺序；公共视频缓存只保存公共资料和计数，个人状态单独查询。

这些模块按当前应用结构接入，没有依赖 GCFeed 的 RabbitMQ、推荐或审核模块。关注流、热榜、异步消息分发和私信在后续阶段实现。
