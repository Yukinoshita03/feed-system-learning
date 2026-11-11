package video

import (
	"context"
	"fmt"
	"time"

	"feed-system-learning/internal/cache"

	"gorm.io/gorm"
)

// viewDedupTTL 是去重窗口：同一账号在同一视频上的重复播放只计一次。
const viewDedupTTL = 5 * time.Minute

// ViewRepository 负责播放明细和 videos.view_count 冗余计数。
type ViewRepository struct {
	db     *gorm.DB
	cache  *cache.Client
	videos *VideoRepository
}

func NewViewRepository(db *gorm.DB, videos *VideoRepository) *ViewRepository {
	return &ViewRepository{db: db, cache: videos.cache, videos: videos}
}

// Record 记录一次播放。返回 true 表示这次计入播放量，false 表示命中去重窗口。
//
// 先去重再写库：Redis 标记是闸门，MySQL 是事实。标记写成功但事务失败时，
// 这次播放不会计数，5 分钟后窗口到期，用户再看一次即可重新计入——丢失范围有界。
// 反过来先写库再打标记，并发请求会同时通过判定各写一条，去重就失效了。
func (r *ViewRepository) Record(ctx context.Context, videoID, accountID uint) (bool, error) {
	if !r.cache.MarkOnce(ctx, cache.ViewDedupKey(videoID, accountID), viewDedupTTL) {
		return false, nil
	}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 用 UPDATE 的受影响行数判断视频是否存在，省掉一次 SELECT。
		result := tx.Model(&Video{}).Where("id = ?", videoID).
			UpdateColumn("view_count", gorm.Expr("view_count + 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Create(&VideoView{VideoID: videoID, AccountID: accountID}).Error
	})
	if err != nil {
		// 写库失败（含视频不存在）时释放标记。否则脏 ID 和临时故障会在
		// 整个去重窗口内被当成"重复播放"，连 404 都返回不出来。
		r.cache.Forget(ctx, cache.ViewDedupKey(videoID, accountID))
		return false, err
	}
	return true, nil
}

// CleanOrphans 分批删除指向已删除视频的播放明细，返回本轮删除的行数。
//
// 播放上报不与视频删除抢视频行的锁（高频写不应串行化），所以"删视频"的瞬间
// 仍可能插入一条孤儿明细，删除路径的清理覆盖不了这个极窄窗口，只能靠定期
// 扫描收敛。外层用单表 DELETE 才支持 LIMIT——MySQL 不允许在多表 DELETE
// 上带 LIMIT；NOT EXISTS 的相关子查询能走 videos 主键索引，避免全表否定扫描。
func (r *ViewRepository) CleanOrphans(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("清理数量必须大于 0")
	}
	result := r.db.WithContext(ctx).Exec(
		"DELETE FROM video_views WHERE NOT EXISTS (SELECT 1 FROM videos WHERE videos.id = video_views.video_id) LIMIT ?",
		limit,
	)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
