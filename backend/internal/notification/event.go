package notification

type LikeCreatedEvent struct {
	LikeID     uint `json:"like_id"`
	VideoID    uint `json:"video_id"`
	ActorID    uint `json:"actor_id"`
	ReceiverID uint `json:"receiver_id"`
}
