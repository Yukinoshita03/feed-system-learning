import { postJson } from './client'

export type Notification = {
  id: number
  receiver_id: number
  actor_id: number
  actor_username: string
  actor_avatar_url: string
  kind: 'like' | 'comment' | 'favorite' | 'follow'
  video_id: number
  content: string
  is_read: boolean
  created_at: string
}

export function list(beforeId = 0) {
  return postJson<{ messages: Notification[]; next_id: number; has_more: boolean }>(
    '/notification/list', { before_id: beforeId }, { authRequired: true },
  )
}

export function unreadCount() {
  return postJson<{ unread_count: number }>('/notification/unreadCount', {}, { authRequired: true })
}

export function markRead(messageIds: number[]) {
  return postJson<{ updated_count: number }>('/notification/markRead', { message_ids: messageIds }, { authRequired: true })
}

export function markAllRead() {
  return postJson<{ updated_count: number }>('/notification/markRead', { all: true }, { authRequired: true })
}

export function remove(messageId: number) {
  return postJson('/notification/delete', { message_id: messageId }, { authRequired: true })
}
