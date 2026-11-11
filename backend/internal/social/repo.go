package social

import (
	"context"
	"encoding/json"
	"errors"
	"feed-system-learning/internal/account"
	"feed-system-learning/internal/notification"
	"fmt"
	"math/rand/v2"
	"sort"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// SetFollowing 用关注关系的唯一索引仲裁并发变更；账号存在性检查不锁账号行。
func (r *Repository) SetFollowing(ctx context.Context, actorID, targetID uint, active bool) (FollowResult, error) {
	var result FollowResult
	accountIDs := []uint{actorID}
	if targetID != actorID {
		accountIDs = append(accountIDs, targetID)
	}
	var foundAccountIDs []uint
	if err := r.db.WithContext(ctx).Model(&account.Account{}).
		Where("id IN ?", accountIDs).Pluck("id", &foundAccountIDs).Error; err != nil {
		return result, err
	}
	if len(foundAccountIDs) != len(accountIDs) {
		return result, gorm.ErrRecordNotFound
	}

	err := retryMySQLDeadlocks(ctx, func() error {
		return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := ensureRelationStats(tx, accountIDs); err != nil {
				return err
			}

			if active {
				relationship := Follow{FollowerID: actorID, VloggerID: targetID}
				if err := tx.Create(&relationship).Error; err != nil {
					if !isDuplicateKeyError(err) {
						return err
					}
					// 联合唯一索引保证重复关注不会创建重复关系。
				} else {
					if targetID != actorID {
						payload, err := json.Marshal(notification.FollowCreatedEvent{
							FollowID:   relationship.ID,
							ActorID:    actorID,
							ReceiverID: targetID,
						})
						if err != nil {
							return err
						}
						outbox := notification.OutboxMessage{
							EventKey:  fmt.Sprintf("follow:%d", relationship.ID),
							EventType: "follow.created",
							Payload:   payload,
						}
						if err := tx.Create(&outbox).Error; err != nil {
							return err
						}
					}
					if err := updateRelationStats(tx, actorID, targetID, 1); err != nil {
						return err
					}
				}
			} else {
				deleted := tx.Where("follower_id = ? AND vlogger_id = ?", actorID, targetID).
					Delete(&Follow{})
				if deleted.Error != nil {
					return deleted.Error
				}
				if deleted.RowsAffected > 0 {
					if err := updateRelationStats(tx, actorID, targetID, -1); err != nil {
						return err
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return result, err
	}
	result.IsFollowing = active

	// 关系事务提交后，在一次查询中读取两个计数。
	stats, err := loadRelationStats(r.db.WithContext(ctx), accountIDs)
	if err != nil {
		return result, err
	}
	result.FollowingCount = stats[actorID].FollowingCount
	result.FollowerCount = stats[targetID].FollowerCount
	return result, nil
}

func isMySQLDeadlockError(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1213
}

// retryMySQLDeadlocks 仅重试 MySQL 的 1213 死锁错误；每次重试都会重新执行整个事务。
func retryMySQLDeadlocks(ctx context.Context, operation func() error) error {
	const maxAttempts = 8
	for attempt := 0; ; attempt++ {
		err := operation()
		if !isMySQLDeadlockError(err) || attempt == maxAttempts-1 {
			return err
		}

		backoff := time.Duration(2<<attempt)*time.Millisecond + time.Duration(rand.IntN(8))*time.Millisecond
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func ensureRelationStats(tx *gorm.DB, userIDs []uint) error {
	userIDs = orderedUniqueIDs(userIDs)
	var existing []RelationStat
	if err := tx.Where("user_id IN ?", userIDs).Find(&existing).Error; err != nil {
		return err
	}
	byID := make(map[uint]*RelationStat, len(existing))
	for i := range existing {
		byID[existing[i].UserID] = &existing[i]
	}

	for _, id := range userIDs {
		stat, found := byID[id]
		if !found {
			stat = &RelationStat{UserID: id}
			// 直接 INSERT 后再处理重复键可能导致死锁：InnoDB 会在重复键检查时加共享锁，
			// 并发事务随后都申请排他锁时可能互相等待。这里用 upsert 处理冲突，
			// 让冲突路径直接获取该行的排他锁。
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(stat).Error; err != nil {
				return err
			}
			var current RelationStat
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, id).Error; err != nil {
				return err
			}
			stat = &current
		} else if !stat.Initialized {
			var current RelationStat
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, id).Error; err != nil {
				return err
			}
			stat = &current
			byID[id] = stat
		}

		if !stat.Initialized {
			if err := tx.Model(&Follow{}).Where("follower_id = ?", id).Count(&stat.FollowingCount).Error; err != nil {
				return err
			}
			if err := tx.Model(&Follow{}).Where("vlogger_id = ?", id).Count(&stat.FollowerCount).Error; err != nil {
				return err
			}
			stat.Initialized = true
			if err := tx.Model(&RelationStat{}).Where("user_id = ?", id).Updates(map[string]any{
				"following_count": stat.FollowingCount,
				"follower_count":  stat.FollowerCount,
				"initialized":     true,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func updateRelationStats(tx *gorm.DB, actorID, targetID uint, delta int64) error {
	if actorID == targetID {
		updates := map[string]any{}
		if delta > 0 {
			updates["following_count"] = gorm.Expr("following_count + ?", delta)
			updates["follower_count"] = gorm.Expr("follower_count + ?", delta)
		} else {
			updates["following_count"] = gorm.Expr("GREATEST(following_count + ?, 0)", delta)
			updates["follower_count"] = gorm.Expr("GREATEST(follower_count + ?, 0)", delta)
		}
		return tx.Model(&RelationStat{}).Where("user_id = ?", actorID).UpdateColumns(updates).Error
	}

	ids := orderedUniqueIDs([]uint{actorID, targetID})
	for _, id := range ids {
		column := "following_count"
		if id == targetID {
			column = "follower_count"
		}
		expression := gorm.Expr(column+" + ?", delta)
		if delta < 0 {
			expression = gorm.Expr("GREATEST("+column+" + ?, 0)", delta)
		}
		updated := tx.Model(&RelationStat{}).Where("user_id = ?", id).UpdateColumn(column, expression)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
	}
	return nil
}

func loadRelationStats(db *gorm.DB, userIDs []uint) (map[uint]RelationStat, error) {
	var rows []RelationStat
	if err := db.Where("user_id IN ?", userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	stats := make(map[uint]RelationStat, len(rows))
	for _, row := range rows {
		stats[row.UserID] = row
	}
	if len(stats) != len(orderedUniqueIDs(userIDs)) {
		return nil, gorm.ErrRecordNotFound
	}
	return stats, nil
}

func orderedUniqueIDs(ids []uint) []uint {
	unique := make([]uint, 0, len(ids))
	for _, id := range ids {
		found := false
		for _, existing := range unique {
			if existing == id {
				found = true
				break
			}
		}
		if !found {
			unique = append(unique, id)
		}
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	return unique
}

func isDuplicateKeyError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func (r *Repository) IsFollowing(ctx context.Context, actorID, targetID uint) (bool, error) {
	var user account.Account
	if err := r.db.WithContext(ctx).Select("id").First(&user, targetID).Error; err != nil {
		return false, err
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&Follow{}).Where("follower_id = ? AND vlogger_id = ?", actorID, targetID).Count(&count).Error
	return count > 0, err
}

// List 只返回公开资料；followers=true 查询粉丝，否则查询关注的人。
func (r *Repository) List(ctx context.Context, userID, beforeID uint, limit int, followers bool) ([]User, int64, error) {
	var user account.Account
	if err := r.db.WithContext(ctx).Select("id").First(&user, userID).Error; err != nil {
		return nil, 0, err
	}
	filter, joined := "follows.follower_id", "follows.vlogger_id"
	if followers {
		filter, joined = "follows.vlogger_id", "follows.follower_id"
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&Follow{}).Where(filter+" = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]User, 0)
	query := r.db.WithContext(ctx).Model(&Follow{}).
		Select("accounts.id, accounts.username, accounts.avatar_url, accounts.bio, follows.id AS follow_cursor").
		Joins("JOIN accounts ON accounts.id = "+joined).Where(filter+" = ?", userID)
	if beforeID > 0 {
		query = query.Where("follows.id < ?", beforeID)
	}
	err := query.Order("follows.id DESC").Limit(limit).Scan(&items).Error
	return items, total, err
}
