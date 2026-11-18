<script setup lang="ts">
import { computed, onMounted, reactive, watch } from 'vue'

import AppShell from '../components/AppShell.vue'
import JsonBox from '../components/JsonBox.vue'
import FeedVideoCard from '../components/FeedVideoCard.vue'
import { ApiError } from '../api/client'
import * as feedApi from '../api/feed'
import * as likeApi from '../api/like'
import type { FeedVideoItem } from '../api/types'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()

type ListState = {
  loading: boolean
  error: string
  items: FeedVideoItem[]
  has_more: boolean
}

const latest = reactive<ListState & { limit: number; next_time: number; next_id: number }>({
  loading: false,
  error: '',
  items: [],
  has_more: false,
  limit: 10,
  next_time: 0,
  next_id: 0,
})

const likesCount = reactive<ListState & { limit: number; window: '24h' | '7d' }>({
  loading: false,
  error: '',
  items: [],
  has_more: false,
  limit: 10,
  window: '24h',
})

const following = reactive<ListState & { limit: number; next_time: number; next_id: number }>({
  loading: false,
  error: '',
  items: [],
  has_more: false,
  limit: 10,
  next_time: 0,
  next_id: 0,
})
let followingGeneration = 0

const action = reactive<{ loading: boolean; error: string; payload: unknown; name: string }>({
  loading: false,
  error: '',
  payload: null,
  name: '',
})

const canLike = computed(() => auth.isLoggedIn)
const likeBusy = reactive<Record<number, boolean>>({})

async function runAction(name: string, fn: () => Promise<unknown>) {
  action.name = name
  action.loading = true
  action.error = ''
  action.payload = null
  try {
    action.payload = await fn()
  } catch (e) {
    action.error = e instanceof ApiError ? e.message : String(e)
    action.payload = e instanceof ApiError ? e.payload : null
  } finally {
    action.loading = false
  }
}

async function loadLatest(reset: boolean) {
  latest.loading = true
  latest.error = ''
  try {
    const latest_time = reset ? 0 : latest.next_time
    const latest_id = reset ? 0 : latest.next_id
    const res = await feedApi.listLatest({ limit: latest.limit, latest_time, latest_id })
    latest.has_more = res.has_more
    latest.next_time = res.next_time
    latest.next_id = res.next_id
    latest.items = reset ? res.video_list : latest.items.concat(res.video_list)
  } catch (e) {
    latest.error = e instanceof ApiError ? e.message : String(e)
  } finally {
    latest.loading = false
  }
}

async function loadLikesCount() {
  likesCount.loading = true
  likesCount.error = ''
  try {
    const res = await feedApi.listByLikes({ limit: likesCount.limit, window: likesCount.window })
    likesCount.items = res.video_list
  } catch (e) {
    likesCount.error = e instanceof ApiError ? e.message : String(e)
  } finally {
    likesCount.loading = false
  }
}

async function loadFollowing(reset: boolean) {
  if (following.loading) return
  const generation = followingGeneration
  following.loading = true
  following.error = ''
  try {
    const latest_time = reset ? 0 : following.next_time
    const latest_id = reset ? 0 : following.next_id
    const res = await feedApi.listFollowing({ limit: following.limit, latest_time, latest_id })
    if (generation !== followingGeneration) return
    following.has_more = res.has_more
    following.next_time = res.next_time
    following.next_id = res.next_id
    following.items = reset ? res.video_list : following.items.concat(res.video_list)
  } catch (e) {
    if (generation === followingGeneration) following.error = e instanceof ApiError ? e.message : String(e)
  } finally {
    if (generation === followingGeneration) following.loading = false
  }
}

async function toggleLike(item: FeedVideoItem) {
  if (!auth.isLoggedIn || likeBusy[item.id]) return
  likeBusy[item.id] = true
  const wasLiked = item.is_liked
  const oldCount = item.likes_count
  const desired = !wasLiked
  item.is_liked = desired
  item.likes_count = Math.max(0, oldCount + (desired ? 1 : -1))
  try {
    await runAction(desired ? '点赞' : '取消点赞', async () => {
      try {
        const result = desired ? await likeApi.like(item.id) : await likeApi.unlike(item.id)
        item.is_liked = result.is_liked
        if (result.is_liked !== desired) item.likes_count = oldCount
        return result
      } catch (error) {
        item.is_liked = wasLiked
        item.likes_count = oldCount
        throw error
      }
    })
  } finally { likeBusy[item.id] = false }
}

onMounted(async () => {
  await loadLatest(true)
  await loadLikesCount()
  if (auth.isLoggedIn) {
    await loadFollowing(true)
  }
})

watch(() => auth.claims?.account_id ?? 0, (accountID) => {
  followingGeneration += 1
  following.items = []
  following.loading = false
  following.error = ''
  following.has_more = false
  following.next_time = 0
  following.next_id = 0
  if (accountID) void loadFollowing(true)
}, { flush: 'sync' })
</script>

<template>
  <AppShell>
    <div class="grid two">
      <div class="card">
        <p class="title">Feed</p>
        <p class="subtle">最新流和点赞榜支持匿名浏览，关注流需要登录。</p>

        <div class="card" style="margin-top: 12px">
          <div class="row" style="justify-content: space-between">
            <div>
              <p class="title">最新流（listLatest）</p>
              <div class="subtle">limit：{{ latest.limit }} · next=(time={{ latest.next_time }}, id={{ latest.next_id }}) · has_more：{{ latest.has_more }}</div>
            </div>
            <div class="row">
              <label class="subtle" style="margin: 0">limit</label>
              <input v-model.number="latest.limit" type="number" min="1" max="50" style="width: 90px" />
              <button class="primary" type="button" :disabled="latest.loading" @click="loadLatest(true)">刷新</button>
              <button type="button" :disabled="latest.loading || !latest.has_more" @click="loadLatest(false)">加载更多</button>
            </div>
          </div>
          <div v-if="latest.error" class="pill bad" style="margin-top: 10px">错误：{{ latest.error }}</div>
          <div class="grid" style="gap: 10px; margin-top: 12px">
            <FeedVideoCard
              v-for="item in latest.items"
              :key="`latest-${item.id}`"
              :item="item"
              :can-like="canLike"
              :busy="action.loading"
              @toggle-like="toggleLike"
            />
          </div>
        </div>

        <div class="card" style="margin-top: 12px">
          <div class="row" style="justify-content: space-between">
            <div>
              <p class="title">点赞榜</p>
              <div class="subtle">
                显示数量：{{ likesCount.limit }}
              </div>
            </div>
            <div class="row">
              <label class="subtle" style="margin: 0">limit</label>
              <input v-model.number="likesCount.limit" type="number" min="1" max="50" style="width: 90px" />
              <select v-model="likesCount.window" :disabled="likesCount.loading" aria-label="点赞榜时间范围">
                <option value="24h">24 小时</option>
                <option value="7d">7 天</option>
              </select>
              <button class="primary" type="button" :disabled="likesCount.loading" @click="loadLikesCount()">刷新</button>
            </div>
          </div>
          <div v-if="likesCount.error" class="pill bad" style="margin-top: 10px">错误：{{ likesCount.error }}</div>
          <div class="grid" style="gap: 10px; margin-top: 12px">
            <FeedVideoCard
              v-for="item in likesCount.items"
              :key="`likes-${item.id}`"
              :item="item"
              :can-like="canLike"
              :busy="action.loading"
              @toggle-like="toggleLike"
            />
          </div>
        </div>

        <div class="card" style="margin-top: 12px">
          <div class="row" style="justify-content: space-between">
            <div>
              <p class="title">关注流</p>
              <div class="subtle">
                limit：{{ following.limit }} · next=(time={{ following.next_time }}, id={{ following.next_id }}) · has_more：{{ following.has_more }}
              </div>
            </div>
            <div class="row">
              <label class="subtle" style="margin: 0">limit</label>
              <input v-model.number="following.limit" type="number" min="1" max="50" style="width: 90px" />
              <button class="primary" type="button" :disabled="following.loading" @click="loadFollowing(true)">刷新</button>
              <button type="button" :disabled="following.loading || !following.has_more" @click="loadFollowing(false)">加载更多</button>
            </div>
          </div>
          <div v-if="!auth.isLoggedIn" class="pill bad" style="margin-top: 10px">未登录：无法访问关注流</div>
          <div v-if="following.error" class="pill bad" style="margin-top: 10px">错误：{{ following.error }}</div>
          <div class="grid" style="gap: 10px; margin-top: 12px">
            <FeedVideoCard
              v-for="item in following.items"
              :key="`following-${item.id}`"
              :item="item"
              :can-like="canLike"
              :busy="action.loading"
              @toggle-like="toggleLike"
            />
          </div>
        </div>
      </div>

      <div class="card">
        <p class="title">动作输出（点赞等）</p>
        <div class="row" style="margin-bottom: 10px">
          <span class="pill">动作：{{ action.name || '-' }}</span>
          <span v-if="action.loading" class="pill">请求中…</span>
          <span v-if="action.error" class="pill bad">错误：{{ action.error }}</span>
        </div>
        <JsonBox :value="action.payload" />
      </div>
    </div>
  </AppShell>
</template>
