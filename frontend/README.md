# Feed System 学习项目前端

Vue 3 + Vite 前端。默认通过 Vite 代理把 `/api/...` 转发到 `http://127.0.0.1:8081/...`，并去掉 `/api` 前缀。完整启动和 Selenium 测试命令见 [运行与测试指令](../docs/运行与测试指令.md)。

## 页面

| 路由 | 页面 | 说明 |
|------|------|------|
| `/` | 推荐页 | 沉浸式短视频播放，支持推荐、关注、点赞榜三个 Tab，本地搜索标题/作者，滚动加载。 |
| `/hot` | 热榜 | 对接 `/feed/listByPopularity`，支持刷新与加载更多。 |
| `/video` | 发布页 | 登录后普通上传 MP4 和封面，再提交发布；分片上传接口待实现。 |
| `/video/:id` | 视频详情 | 视频播放、点赞、评论抽屉、作者信息。 |
| `/account` | 账号页 | 登录、个人信息、粉丝/关注入口。 |
| `/account/register` | 注册页 | 创建账号。 |
| `/account/change-password` | 改密页 | 使用旧密码修改密码。 |
| `/settings` | 设置页 | 改名、退出登录、跳转改密。 |
| `/u/:id` | 用户主页 | 用户作品、粉丝/关注列表、关注/取关、私信入口。 |
| `/messages` | 私信联系人 | 从粉丝和关注用户中选择聊天对象。 |
| `/messages/:peerId` | 私信会话 | 查看最近 50 条私信并发送消息。 |

## 前端能力

账号、视频发布/播放、首页最新视频流和点赞已接入当前后端。首页按时间和 ID 双游标加载下一页，保留滚动占位并只挂载当前及相邻的播放内容；首页、详情页的红心和计数采用服务端返回值，账号页支持查看我赞过的视频。点赞榜、关注、评论、私信等接口仍待实现。

- Pinia 保存 access token 与 refresh token。
- API Client 在 401 时自动调用 `/account/refresh` 并重试原请求。
- 发布页默认使用普通上传；保留的分片流程使用 `spark-md5`，待后端分片接口完成后通过 `VITE_ENABLE_CHUNK_UPLOAD=true` 启用。
- 播放页使用虚拟渲染窗口，只保留当前视频前后各一条 DOM，降低长列表播放成本。
- 点赞、关注、评论、分享等交互通过 toast 提示结果。
- `API_PROXY_TARGET` 设置 Vite 转发到的后端地址；`VITE_API_BASE` 设置浏览器请求前缀，默认 `/api`。

## 开发启动

先按 [运行与测试指令](../docs/运行与测试指令.md) 启动本机 MySQL、Redis 和后端 API。

在仓库根目录启动前端：

```bash
cd frontend
npm ci
API_PROXY_TARGET='http://127.0.0.1:8081' VITE_ENABLE_CHUNK_UPLOAD='false' \
  npm run dev -- --host 127.0.0.1 --port 5173 --strictPort
```

打开 <http://127.0.0.1:5173>。

## 构建

```bash
npm run build
```

构建产物位于 `frontend/dist/`。部署时还需配置 `/api` 的反向代理；Vite 开发代理仅在开发服务运行时生效。
