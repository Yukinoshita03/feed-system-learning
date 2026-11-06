import { postJson } from './client'
import type { IsLikedResponse, LikeResponse, Video } from './types'

export function like(videoId: number) {
  return postJson<LikeResponse>('/like/like', { video_id: videoId }, { authRequired: true })
}

export function unlike(videoId: number) {
  return postJson<LikeResponse>('/like/unlike', { video_id: videoId }, { authRequired: true })
}

export function isLiked(videoId: number) {
  return postJson<IsLikedResponse>('/like/isLiked', { video_id: videoId }, { authRequired: true })
}

export function listMyLikedVideos() {
  return postJson<Video[]>('/like/listMyLikedVideos', {}, { authRequired: true })
}
