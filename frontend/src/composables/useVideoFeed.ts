import { computed, reactive, ref, watch } from 'vue'
import { ApiError } from '../api/client'
import * as feedApi from '../api/feed'
import type { FeedVideoItem } from '../api/types'
import { useAuthStore } from '../stores/auth'

export type TabKey = 'recommend' | 'hot' | 'following'

export function useVideoFeed() {
  const auth = useAuthStore()
  const tab = ref<TabKey>('recommend')

  const recommend = reactive({
    items: [] as FeedVideoItem[],
    loading: false, error: '',
    hasMore: false, nextTime: 0, nextId: 0,
  })

  const hot = reactive({
    items: [] as FeedVideoItem[],
    loading: false, error: '',
    hasMore: false,
  })

  const following = reactive({
    items: [] as FeedVideoItem[],
    loading: false, error: '',
    hasMore: false, nextTime: 0, nextId: 0,
  })

  let followingGeneration = 0
  watch(() => auth.claims?.account_id ?? 0, () => {
    followingGeneration += 1
    following.items = []
    following.loading = false
    following.error = ''
    following.hasMore = false
    following.nextTime = 0
    following.nextId = 0
  }, { flush: 'sync' })

  const currentState = computed(() => {
    if (tab.value === 'hot') return hot
    if (tab.value === 'following') return following
    return recommend
  })

  async function loadRecommend(reset: boolean) {
    if (recommend.loading) return
    recommend.loading = true
    recommend.error = ''
    try {
      const res = await feedApi.listLatest({
        limit: 10,
        latest_time: reset ? 0 : recommend.nextTime,
        latest_id: reset ? 0 : recommend.nextId,
      })
      recommend.hasMore = res.has_more
      recommend.nextTime = res.next_time
      recommend.nextId = res.next_id
      recommend.items = reset ? res.video_list : recommend.items.concat(res.video_list)
    } catch (e) {
      recommend.error = e instanceof ApiError ? e.message : String(e)
    } finally {
      recommend.loading = false
    }
  }

  async function loadHot() {
    if (hot.loading) return
    hot.loading = true
    hot.error = ''
    try {
      const res = await feedApi.listByLikes({ limit: 50, window: '24h' })
      hot.hasMore = false
      hot.items = res.video_list
    } catch (e) {
      hot.error = e instanceof ApiError ? e.message : String(e)
    } finally {
      hot.loading = false
    }
  }

  async function loadFollowing(reset: boolean) {
    if (!auth.isLoggedIn) {
      following.error = '登录后才能查看关注流'
      return
    }
    if (following.loading) return
    const generation = followingGeneration
    following.loading = true
    following.error = ''
    try {
      const res = await feedApi.listFollowing({
        limit: 10,
        latest_time: reset ? 0 : following.nextTime,
        latest_id: reset ? 0 : following.nextId,
      })
      if (generation !== followingGeneration) return
      following.hasMore = res.has_more
      following.nextTime = res.next_time
      following.nextId = res.next_id
      following.items = reset ? res.video_list : following.items.concat(res.video_list)
    } catch (e) {
      if (generation === followingGeneration) following.error = e instanceof ApiError ? e.message : String(e)
    } finally {
      if (generation === followingGeneration) following.loading = false
    }
  }

  async function ensureTabLoaded() {
    if (tab.value === 'recommend' && recommend.items.length === 0) await loadRecommend(true)
    if (tab.value === 'hot' && hot.items.length === 0) await loadHot()
    if (tab.value === 'following' && following.items.length === 0) await loadFollowing(true)
  }

  async function loadMoreIfNeeded(activeIndex: number) {
    const items = currentState.value.items
    if (items.length === 0) return
    if (activeIndex < items.length - 3) return
    if (tab.value === 'recommend' && recommend.hasMore) await loadRecommend(false)
    if (tab.value === 'following' && following.hasMore) await loadFollowing(false)
  }

  return { tab, recommend, hot, following, currentState, loadRecommend, loadHot, loadFollowing, ensureTabLoaded, loadMoreIfNeeded }
}
