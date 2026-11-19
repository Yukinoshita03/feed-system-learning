import { postJson } from './client'
import { normalizeFeedVideoList } from './normalize'
import type { ListLatestResponse, ListRankedResponse } from './types'

type FeedCursor = { limit: number; latest_time: number; latest_id: number }
type RankingRequest = { limit: number; window: '24h' | '7d' }

export async function listLatest(input: FeedCursor) {
  const res = await postJson<ListLatestResponse>('/feed/listLatest', input)
  return { ...res, video_list: normalizeFeedVideoList(res.video_list) }
}

export async function listByLikes(input: RankingRequest) {
  const res = await postJson<ListRankedResponse>('/feed/listByLikes', input)
  return { ...res, video_list: normalizeFeedVideoList(res.video_list) }
}

export async function listByHot(input: RankingRequest) {
  const res = await postJson<ListRankedResponse>('/feed/listByHot', input)
  return { ...res, video_list: normalizeFeedVideoList(res.video_list) }
}

export async function listFollowing(input: FeedCursor) {
  const res = await postJson<ListLatestResponse>('/feed/listFollowing', input, { authRequired: true })
  return { ...res, video_list: normalizeFeedVideoList(res.video_list) }
}
