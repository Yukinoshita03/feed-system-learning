import { postJson } from './client'
import { normalizeCommentList } from './normalize'
import type { Comment, CommentPage, MessageResponse } from './types'

export async function listPage(videoId: number, beforeId = 0) {
  const page = await postJson<CommentPage>('/comment/list', { video_id: videoId, before_id: beforeId, limit: 20 })
  return { ...page, comments: normalizeCommentList(page.comments) }
}

export async function listAll(videoId: number) {
  const comments = await postJson<Comment[] | null>('/comment/listAll', { video_id: videoId })
  return normalizeCommentList(comments)
}

export function publish(videoId: number, content: string) {
  return postJson<MessageResponse>('/comment/publish', { video_id: videoId, content }, { authRequired: true })
}

export function remove(commentId: number) {
  return postJson<MessageResponse>('/comment/delete', { comment_id: commentId }, { authRequired: true })
}
