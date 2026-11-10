<script setup lang="ts">
import { onMounted, reactive } from 'vue'
import { useRouter } from 'vue-router'
import AppShell from '../components/AppShell.vue'
import { ApiError } from '../api/client'
import * as notificationApi from '../api/notification'
import type { Notification } from '../api/notification'

const router = useRouter()
const state = reactive({ loading: false, error: '', items: [] as Notification[], nextId: 0, hasMore: false, unread: 0 })

function showError(error: unknown) { state.error = error instanceof ApiError ? error.message : String(error) }

async function load(append = false) {
  if (state.loading) return
  state.loading = true
  state.error = ''
  try {
    const [page, count] = await Promise.all([notificationApi.list(append ? state.nextId : 0), notificationApi.unreadCount()])
    state.items = append ? [...state.items, ...page.messages] : page.messages
    state.nextId = page.next_id
    state.hasMore = page.has_more
    state.unread = count.unread_count
  } catch (e) { showError(e) }
  finally { state.loading = false }
}

async function read(item: Notification) {
  if (state.loading) return
  state.loading = true
  state.error = ''
  try { await notificationApi.markRead([item.id]); item.is_read = true; state.unread = (await notificationApi.unreadCount()).unread_count }
  catch (e) { showError(e) }
  finally { state.loading = false }
}

async function readAll() {
  if (state.loading) return
  state.loading = true
  state.error = ''
  try { await notificationApi.markAllRead(); state.items.forEach(item => { item.is_read = true }); state.unread = (await notificationApi.unreadCount()).unread_count }
  catch (e) { showError(e) }
  finally { state.loading = false }
}

async function remove(item: Notification) {
  if (state.loading) return
  state.loading = true
  state.error = ''
  try { await notificationApi.remove(item.id); state.items = state.items.filter(current => current.id !== item.id); state.unread = (await notificationApi.unreadCount()).unread_count }
  catch (e) { showError(e) }
  finally { state.loading = false }
}

onMounted(() => load())
</script>

<template>
  <AppShell>
    <section class="card notifications">
      <div class="row" style="justify-content: space-between">
        <h2>互动通知 <span class="subtle" data-testid="unread-count">未读 {{ state.unread }}</span></h2>
        <div class="row">
          <button class="ghost" type="button" :disabled="state.loading" @click="load()">刷新</button>
          <button class="primary" type="button" :disabled="state.loading || state.unread === 0" @click="readAll">全部已读</button>
        </div>
      </div>
      <p v-if="state.error" class="error" role="alert">{{ state.error }}</p>
      <p v-if="!state.loading && !state.items.length" class="subtle">暂无通知</p>
      <article v-for="item in state.items" :key="item.id" class="notice" :class="{ unread: !item.is_read }" :data-id="item.id">
        <div class="row" style="justify-content: space-between">
          <RouterLink :to="`/u/${item.actor_id}`">@{{ item.actor_username || '用户' }}</RouterLink>
          <span class="subtle">{{ new Date(item.created_at).toLocaleString() }}</span>
        </div>
        <p>{{ item.content }}</p>
        <div class="row">
          <button v-if="item.video_id" class="ghost" type="button" @click="router.push(`/video/${item.video_id}`)">查看视频</button>
          <button v-if="!item.is_read" class="ghost" type="button" :disabled="state.loading" @click="read(item)">标记已读</button>
          <button class="ghost" type="button" :disabled="state.loading" @click="remove(item)">删除通知</button>
        </div>
      </article>
      <button v-if="state.hasMore" class="ghost" type="button" :disabled="state.loading" @click="load(true)">加载更多通知</button>
      <p v-if="state.loading" class="subtle">加载中…</p>
    </section>
  </AppShell>
</template>

<style scoped>
.notifications { max-width: 900px; margin: 0 auto; }
h2 { font-size: 20px; }
.notice { margin: 14px 0; padding: 16px; border: 1px solid rgba(255,255,255,.15); border-radius: 12px; }
.notice.unread { border-color: #fe2c55; }
.notice p { overflow-wrap: anywhere; white-space: pre-wrap; }
.error { color: #ff6988; }
</style>
