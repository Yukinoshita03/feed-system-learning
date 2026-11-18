export type MessageResponse = { message: string }

export type DirectMessage = {
  id: number
  from_id: number
  to_id: number
  content: string
  is_read: boolean
  created_at: string
}

export type ListMessagesResponse = {
  messages: DirectMessage[]
}

export type TokenResponse = { token: string; refresh_token?: string; account_id?: number; username?: string }

export type Account = {
  id: number
  username: string
  avatar_url?: string
  bio?: string
}

export type Video = {
  id: number
  author_id: number
  username: string
  title: string
  description?: string
  play_url: string
  cover_url: string
  create_time: string
  likes_count: number
  favorites_count?: number
  comments_count?: number
}

export type CommentPage = { comments: Comment[]; next_id: number; has_more: boolean }

export type Comment = {
  id: number
  username: string
  video_id: number
  author_id: number
  content: string
  created_at: string
}

export type FeedAuthor = {
  id: number
  username: string
}

export type FeedVideoItem = {
  id: number
  author: FeedAuthor
  title: string
  description?: string
  play_url: string
  cover_url: string
  create_time: number
  likes_count: number
  is_liked: boolean
}

export type ListLatestResponse = {
  video_list: FeedVideoItem[]
  next_time: number
  next_id: number
  has_more: boolean
}

export type ListRankedResponse = {
  video_list: FeedVideoItem[]
  window: '24h' | '7d'
}

export type IsLikedResponse = {
  is_liked: boolean
}

export type LikeResponse = MessageResponse & {
  is_liked: boolean
  changed: boolean
}

export type GetAllFollowersResponse = {
  followers: Account[]
}

export type GetAllVloggersResponse = {
  vloggers: Account[]
}
