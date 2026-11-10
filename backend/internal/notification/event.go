package notification

type LikeCreatedEvent struct {
	LikeID     uint `json:"like_id"`
	VideoID    uint `json:"video_id"`
	ActorID    uint `json:"actor_id"`
	ReceiverID uint `json:"receiver_id"`
}

type CommentCreatedEvent struct {
	CommentID  uint `json:"comment_id"`
	VideoID    uint `json:"video_id"`
	ActorID    uint `json:"actor_id"`
	ReceiverID uint `json:"receiver_id"`
}
type FavoriteCreatedEvent struct {
	FavoriteID uint `json:"favorite_id"`
	VideoID    uint `json:"video_id"`
	ActorID    uint `json:"actor_id"`
	ReceiverID uint `json:"receiver_id"`
}
type FollowCreatedEvent struct {
	FollowID   uint `json:"follow_id"`
	ActorID    uint `json:"actor_id"`
	ReceiverID uint `json:"receiver_id"`
}
