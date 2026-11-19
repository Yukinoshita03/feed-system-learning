package video

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CounterRepository struct{ db *gorm.DB }

func NewCounterRepository(db *gorm.DB) *CounterRepository { return &CounterRepository{db: db} }

func duplicateKey(err error, key string) bool {
	var mysqlErr *driver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 && strings.Contains(mysqlErr.Message, key)
}

// interactionError 将删除并发导致的真实外键失败转换成视频不存在。
func interactionError(err error) error {
	var mysqlErr *driver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1452 {
		return gorm.ErrRecordNotFound
	}
	return err
}

// ApplyBatch 先在数据库去重，再合并增量；与消费记录在同一事务提交。
func (r *CounterRepository) ApplyBatch(ctx context.Context, events []CounterEvent) error {
	for _, event := range events {
		if err := event.Validate(); err != nil {
			return err
		}
	}
	if len(events) == 0 {
		return nil
	}
	events = append([]CounterEvent(nil), events...)
	sort.Slice(events, func(i, j int) bool { return events[i].EventID < events[j].EventID })
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		deltas := make(map[uint]*VideoCounter)
		for _, event := range events {
			receipt := VideoCounterConsumption{EventID: event.EventID, ProcessedAt: time.Now().UTC()}
			if err := tx.Create(&receipt).Error; err != nil {
				if duplicateKey(err, "PRIMARY") {
					continue
				}
				return err
			}
			if deltas[event.VideoID] == nil {
				deltas[event.VideoID] = &VideoCounter{VideoID: event.VideoID}
			}
			delta := deltas[event.VideoID]
			switch event.Kind {
			case KindLike:
				delta.LikesCount += event.Delta
			case KindFavorite:
				delta.FavoritesCount += event.Delta
			case KindComment:
				delta.CommentsCount += event.Delta
			}
		}
		ids := make([]uint, 0, len(deltas))
		for id := range deltas {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			delta := deltas[id]
			// 相抵为零的事件仍已去重；跳过无必要的行更新。
			if delta.LikesCount == 0 && delta.FavoritesCount == 0 && delta.CommentsCount == 0 {
				continue
			}
			result := tx.Model(&VideoCounter{}).Where("video_id = ?", id).Updates(map[string]any{
				"likes_count":     gorm.Expr("likes_count + ?", delta.LikesCount),
				"favorites_count": gorm.Expr("favorites_count + ?", delta.FavoritesCount),
				"comments_count":  gorm.Expr("comments_count + ?", delta.CommentsCount),
				"updated_at":      time.Now().UTC(),
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				// 当前读判断视频是否已删除，避免 RR 快照误判；迟到事件不重建计数行。
				var current Video
				err := tx.Select("id").Clauses(clause.Locking{Strength: "SHARE"}).First(&current, id).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("视频 %d 的计数投影缺失，请停写修复后重试", id)
			}
		}
		return nil
	})
}

// Fill 一次补齐整页展示计数；元数据缓存里的旧计数始终被覆盖。
func (r *CounterRepository) Fill(ctx context.Context, videos []Video) error {
	if len(videos) == 0 {
		return nil
	}
	ids := make([]uint, len(videos))
	for i, item := range videos {
		ids[i] = item.ID
	}
	var counters []VideoCounter
	if err := r.db.WithContext(ctx).Where("video_id IN ?", ids).Find(&counters).Error; err != nil {
		return err
	}
	byID := make(map[uint]VideoCounter, len(counters))
	for _, counter := range counters {
		byID[counter.VideoID] = counter
	}
	// 视频可能在元数据读完后被删除。仅对缺项再批量确认一次，允许这次已读快照
	// 结束；现存视频缺失投影仍然报错，不把迁移错误静默显示成零。
	missing := make([]uint, 0)
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		var existingIDs []uint
		if err := r.db.WithContext(ctx).Model(&Video{}).Where("id IN ?", missing).Pluck("id", &existingIDs).Error; err != nil {
			return err
		}
		if len(existingIDs) > 0 {
			return fmt.Errorf("视频 %d 的计数投影缺失，请执行离线计数迁移", existingIDs[0])
		}
	}
	for i := range videos {
		counter := byID[videos[i].ID]
		videos[i].LikesCount = max(0, counter.LikesCount)
		videos[i].FavoritesCount = max(0, counter.FavoritesCount)
		videos[i].CommentsCount = max(0, counter.CommentsCount)
	}
	return nil
}

// CheckReady 防止旧数据库未回填就启动新写路径。
func (r *CounterRepository) CheckReady(ctx context.Context) error {
	var missing int64
	if err := r.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM videos v
		LEFT JOIN video_counters c ON c.video_id = v.id WHERE c.video_id IS NULL`).Scan(&missing).Error; err != nil {
		return err
	}
	if missing > 0 {
		return fmt.Errorf("有 %d 个视频尚未初始化计数：停写后运行 go run ./cmd/migrate-counters -offline", missing)
	}
	return nil
}

// BackfillOffline 只供明确停写后的迁移命令调用，不在启动或请求路径自动重算。
func (r *CounterRepository) BackfillOffline(ctx context.Context) error {
	var pending int64
	if err := r.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM notification_outbox o
		LEFT JOIN video_counter_consumptions c ON c.event_id = o.event_key
		WHERE o.event_type = ? AND c.event_id IS NULL`, CounterEventType).Scan(&pending).Error; err != nil {
		return err
	}
	if pending > 0 {
		return fmt.Errorf("仍有 %d 条未消费计数事件，请先恢复消费并排空后再回填", pending)
	}
	return r.db.WithContext(ctx).Exec(`INSERT INTO video_counters
		(video_id, likes_count, favorites_count, comments_count, updated_at)
		SELECT v.id, COALESCE(l.n, 0), COALESCE(f.n, 0), COALESCE(c.n, 0), UTC_TIMESTAMP(3)
		FROM videos v
		LEFT JOIN (SELECT video_id, COUNT(*) n FROM likes GROUP BY video_id) l ON l.video_id = v.id
		LEFT JOIN (SELECT video_id, COUNT(*) n FROM favorites GROUP BY video_id) f ON f.video_id = v.id
		LEFT JOIN (SELECT video_id, COUNT(*) n FROM comments GROUP BY video_id) c ON c.video_id = v.id
		ON DUPLICATE KEY UPDATE likes_count = VALUES(likes_count),
		favorites_count = VALUES(favorites_count), comments_count = VALUES(comments_count),
		updated_at = VALUES(updated_at)`).Error
}
