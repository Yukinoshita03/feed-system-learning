import { postJson } from './client'
import { normalizeVideoList } from './normalize'
import type { Video } from './types'

type FavoriteResult = { is_favorited: boolean; favorites_count: number }
type FavoritePage = { video_list: Video[]; next_id: number; has_more: boolean }

export function favorite(videoId: number) {
  return postJson<FavoriteResult>('/favorite/favorite', { video_id: videoId }, { authRequired: true })
}

export function unfavorite(videoId: number) {
  return postJson<FavoriteResult>('/favorite/unfavorite', { video_id: videoId }, { authRequired: true })
}

export function isFavorited(videoId: number) {
  return postJson<{ is_favorited: boolean }>('/favorite/isFavorited', { video_id: videoId }, { authRequired: true })
}

export async function listMyFavorites(beforeId = 0) {
  const page = await postJson<FavoritePage>('/favorite/listMyFavorites', { before_id: beforeId }, { authRequired: true })
  return { ...page, video_list: normalizeVideoList(page.video_list) }
}
