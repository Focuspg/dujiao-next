package gormstore

import (
	"time"

	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"

	"gorm.io/gorm"
)

// activeStatuses 已占用上游执行能力的任务状态（用于并发上限）
var activeStatuses = []string{
	directchargedomain.TaskSubmitting,
	directchargedomain.TaskSubmitUnknown,
	directchargedomain.TaskProcessing,
	directchargedomain.TaskRequiresAction,
}

func (s *Store) TasksByStatus(status string, limit int) ([]directchargedomain.Task, error) {
	var tasks []directchargedomain.Task
	err := s.db.Where("status = ?", status).Order("id ASC").Limit(limit).Find(&tasks).Error
	return tasks, err
}

func (s *Store) TasksDue(statuses []string, now time.Time, limit int) ([]directchargedomain.Task, error) {
	var tasks []directchargedomain.Task
	err := s.db.Where("status IN ? AND (next_check_at IS NULL OR next_check_at <= ?)", statuses, now).
		Order("next_check_at ASC, id ASC").Limit(limit).Find(&tasks).Error
	return tasks, err
}

func (s *Store) StaleTasks(status string, before time.Time, limit int) ([]directchargedomain.Task, error) {
	var tasks []directchargedomain.Task
	err := s.db.Where("status = ? AND updated_at < ?", status, before).Order("id ASC").Limit(limit).Find(&tasks).Error
	return tasks, err
}

func (s *Store) CountActive(specID uint) (int64, error) {
	var count int64
	err := s.db.Model(&directchargedomain.Task{}).
		Where("spec_id = ? AND status IN ?", specID, activeStatuses).Count(&count).Error
	return count, err
}

func (s *Store) TransitionTask(change directchargecontract.TaskChange) (bool, error) {
	applied := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		updates := make(map[string]interface{}, len(change.Fields)+5)
		for key, value := range change.Fields {
			updates[key] = value
		}
		updates["status"] = change.ToStatus
		updates["version"] = change.FromVersion + 1
		updates["updated_at"] = change.Now
		if change.NewAttempt != nil {
			if err := tx.Create(change.NewAttempt).Error; err != nil {
				return err
			}
			updates["current_attempt_id"] = change.NewAttempt.ID
			updates["attempt_count"] = gorm.Expr("attempt_count + 1")
		}
		if change.NewCredential != nil {
			if err := tx.Model(&directchargedomain.Credential{}).
				Where("task_id = ? AND destroyed_at IS NULL", change.TaskID).
				Updates(map[string]interface{}{"ciphertext": "", "destroyed_at": change.Now}).Error; err != nil {
				return err
			}
			taskID := change.TaskID
			change.NewCredential.TaskID = &taskID
			if err := tx.Create(change.NewCredential).Error; err != nil {
				return err
			}
			updates["credential_id"] = change.NewCredential.ID
			if change.NewPreflight != nil {
				change.NewPreflight.CredentialID = &change.NewCredential.ID
				if err := tx.Create(change.NewPreflight).Error; err != nil {
					return err
				}
				updates["preflight_id"] = change.NewPreflight.ID
			}
		}
		result := tx.Model(&directchargedomain.Task{}).
			Where("id = ? AND version = ? AND status = ?", change.TaskID, change.FromVersion, change.FromStatus).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			// 版本已变：回滚本事务内新建的尝试
			return errTransitionLost
		}
		applied = true
		if change.Attempt != nil {
			if err := tx.Save(change.Attempt).Error; err != nil {
				return err
			}
		}
		if directchargedomain.IsTerminal(change.ToStatus) {
			if err := tx.Where("task_id = ?", change.TaskID).Delete(&directchargedomain.AccountLock{}).Error; err != nil {
				return err
			}
		}
		if directchargedomain.IsTerminal(change.ToStatus) || change.DestroyCredential {
			if err := tx.Model(&directchargedomain.Credential{}).
				Where("task_id = ? AND destroyed_at IS NULL", change.TaskID).
				Updates(map[string]interface{}{"ciphertext": "", "destroyed_at": change.Now}).Error; err != nil {
				return err
			}
		}
		if change.FromStatus == change.ToStatus && change.Reason == "" {
			return nil
		}
		return tx.Create(&directchargedomain.TaskLog{
			TaskID: change.TaskID, FromStatus: change.FromStatus, ToStatus: change.ToStatus,
			Reason: truncate(change.Reason, 500), Actor: change.Actor, CreatedAt: change.Now,
		}).Error
	})
	if err == errTransitionLost {
		return false, nil
	}
	if err != nil {
		applied = false
	}
	return applied, err
}

func (s *Store) AttemptByID(id uint) (*directchargedomain.Attempt, error) {
	return first[directchargedomain.Attempt](s.db, "id = ?", id)
}

func (s *Store) AttemptByClientRequestID(clientRequestID string) (*directchargedomain.Attempt, error) {
	return first[directchargedomain.Attempt](s.db, "client_request_id = ?", clientRequestID)
}

func (s *Store) AttemptByUpstreamOrderID(upstreamOrderID int64) (*directchargedomain.Attempt, error) {
	return first[directchargedomain.Attempt](s.db, "upstream_order_id = ?", upstreamOrderID)
}

func (s *Store) CredentialByID(id uint) (*directchargedomain.Credential, error) {
	return first[directchargedomain.Credential](s.db, "id = ?", id)
}

func (s *Store) PreflightByID(id uint) (*directchargedomain.Preflight, error) {
	return first[directchargedomain.Preflight](s.db, "id = ?", id)
}

func (s *Store) ReplaceCredentialSecret(credentialID uint, ciphertext string, keyVersion int, preflightID uint, tokenExpiresAt *time.Time) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&directchargedomain.Credential{}).
			Where("id = ? AND destroyed_at IS NULL", credentialID).
			Updates(map[string]interface{}{"ciphertext": ciphertext, "key_version": keyVersion}).Error; err != nil {
			return err
		}
		if preflightID == 0 {
			return nil
		}
		return tx.Model(&directchargedomain.Preflight{}).Where("id = ?", preflightID).
			Update("token_expires_at", tokenExpiresAt).Error
	})
}

// PaidOrdersWithoutTask 直接读订单表：只取代充子订单的编号与状态，不依赖订单模块的模型
func (s *Store) PaidOrdersWithoutTask(limit int) ([]directchargecontract.OrphanOrder, error) {
	var rows []struct {
		ID      uint
		OrderNo string
		Status  string
	}
	err := s.db.Table("orders AS o").
		Select("o.id, o.order_no, o.status").
		Where("o.deleted_at IS NULL AND o.parent_id IS NOT NULL AND o.status IN ?", []string{"paid", "fulfilling"}).
		Where("EXISTS (SELECT 1 FROM order_items i WHERE i.order_id = o.id AND i.fulfillment_type = ?)", "direct_charge").
		// 任务按未付款关闭后订单才到账，同样没有可推进的任务
		Where("NOT EXISTS (SELECT 1 FROM directcharge_tasks t WHERE t.order_id = o.id AND t.status <> ?)", directchargedomain.TaskClosed).
		Order("o.id ASC").Limit(limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]directchargecontract.OrphanOrder, 0, len(rows))
	for _, row := range rows {
		out = append(out, directchargecontract.OrphanOrder{OrderID: row.ID, OrderNo: row.OrderNo, Status: row.Status})
	}
	return out, nil
}

func (s *Store) SaveWebhookEvent(event *directchargedomain.WebhookEvent) (bool, error) {
	existing, err := first[directchargedomain.WebhookEvent](s.db, "source = ? AND dedup_key = ?", event.Source, event.DedupKey)
	if err != nil {
		return false, err
	}
	if existing != nil {
		return false, nil
	}
	if err := s.db.Create(event).Error; err != nil {
		if isUniqueViolation(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) MarkWebhookEvent(id uint, status string, taskID *uint, now time.Time) error {
	return s.db.Model(&directchargedomain.WebhookEvent{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status": status, "task_id": taskID, "processed_at": now, "attempts": gorm.Expr("attempts + 1"),
	}).Error
}

func (s *Store) UnmatchedWebhookEvents(since time.Time, limit int) ([]directchargedomain.WebhookEvent, error) {
	var events []directchargedomain.WebhookEvent
	err := s.db.Where("status = ? AND received_at >= ?", directchargedomain.EventUnmatched, since).
		Order("id ASC").Limit(limit).Find(&events).Error
	return events, err
}

func (s *Store) WakeTask(taskID uint, now time.Time) error {
	return s.db.Model(&directchargedomain.Task{}).
		Where("id = ? AND status IN ?", taskID, activeStatuses).
		Update("next_check_at", now).Error
}

func truncate(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}
