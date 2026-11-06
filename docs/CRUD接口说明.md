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
