import { ApiError, postJson } from './client'
import { normalizeVideoList } from './normalize'
import type { Video } from './types'

type FavoriteResult = { is_favorited: boolean; changed: boolean }
type FavoritePage = { video_list: Video[]; next_id: number; has_more: boolean }

export function favorite(videoId: number) {
  return setFavorited(videoId, true)
}

export function unfavorite(videoId: number) {
  return setFavorited(videoId, false)
}

async function setFavorited(videoId: number, desired: boolean): Promise<FavoriteResult> {
  try {
    return await postJson<FavoriteResult>(desired ? '/favorite/favorite' : '/favorite/unfavorite', { video_id: videoId }, { authRequired: true })
  } catch (error) {
    if (!(error instanceof ApiError) || error.status >= 500) {
      try {
        const current = await isFavorited(videoId)
        if (current.is_favorited === desired) return { ...current, changed: false }
      } catch { /* 保留原始请求错误。 */ }
    }
    throw error
  }
}

export function isFavorited(videoId: number) {
  return postJson<{ is_favorited: boolean }>('/favorite/isFavorited', { video_id: videoId }, { authRequired: true })
}

export async function listMyFavorites(beforeId = 0) {
  const page = await postJson<FavoritePage>('/favorite/listMyFavorites', { before_id: beforeId }, { authRequired: true })
  return { ...page, video_list: normalizeVideoList(page.video_list) }
}
