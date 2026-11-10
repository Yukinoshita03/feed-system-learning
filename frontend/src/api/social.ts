import { postJson } from './client'
import { listOrEmpty, normalizeAccount } from './normalize'
import type { GetAllFollowersResponse, GetAllVloggersResponse } from './types'

type FollowResult = { is_following: boolean; following_count: number; follower_count: number }

export function follow(vloggerId: number) {
  return postJson<FollowResult>('/social/follow', { vlogger_id: vloggerId }, { authRequired: true })
}

export function unfollow(vloggerId: number) {
  return postJson<FollowResult>('/social/unfollow', { vlogger_id: vloggerId }, { authRequired: true })
}

// 页面现有列表需要完整关系集合，按服务端游标逐页读取，不受首屏截断影响。
export async function getAllFollowers(vloggerId?: number) {
  let beforeId = 0
  const followers = [] as import('./types').Account[]
  for (;;) {
    const page = await postJson<GetAllFollowersResponse & { next_id: number; has_more: boolean }>(
      '/social/getAllFollowers', { vlogger_id: vloggerId, before_id: beforeId, limit: 100 }, { authRequired: true },
    )
    followers.push(...listOrEmpty(page.followers).map(normalizeAccount))
    if (!page.has_more) return { followers }
    beforeId = page.next_id
  }
}

export async function getAllVloggers(followerId?: number) {
  let beforeId = 0
  const vloggers = [] as import('./types').Account[]
  for (;;) {
    const page = await postJson<GetAllVloggersResponse & { next_id: number; has_more: boolean }>(
      '/social/getAllVloggers', { follower_id: followerId, before_id: beforeId, limit: 100 }, { authRequired: true },
    )
    vloggers.push(...listOrEmpty(page.vloggers).map(normalizeAccount))
    if (!page.has_more) return { vloggers }
    beforeId = page.next_id
  }
}
