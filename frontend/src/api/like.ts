import { ApiError, postJson } from './client'
import type { IsLikedResponse, LikeResponse, Video } from './types'

export function like(videoId: number) {
  return setLiked(videoId, true)
}

export function unlike(videoId: number) {
  return setLiked(videoId, false)
}

async function setLiked(videoId: number, desired: boolean): Promise<LikeResponse> {
  try {
    return await postJson<LikeResponse>(desired ? '/like/like' : '/like/unlike', { video_id: videoId }, { authRequired: true })
  } catch (error) {
    // 网络或服务器错误可能发生在事务提交之后，先查询事实再决定是否回滚页面。
    if (!(error instanceof ApiError) || error.status >= 500) {
      try {
        const current = await isLiked(videoId)
        if (current.is_liked === desired) return { ...current, changed: false, message: '点赞状态已确认' }
      } catch { /* 查询也失败时交给页面提示并恢复旧状态。 */ }
    }
    throw error
  }
}

export function isLiked(videoId: number) {
  return postJson<IsLikedResponse>('/like/isLiked', { video_id: videoId }, { authRequired: true })
}

export function listMyLikedVideos() {
  return postJson<Video[]>('/like/listMyLikedVideos', {}, { authRequired: true })
}
