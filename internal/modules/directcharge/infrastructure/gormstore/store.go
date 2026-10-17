// Package gormstore 是账号代充模块的 GORM 持久化实现。
package gormstore

import (
	"errors"
	"strings"
	"time"

	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"

	"gorm.io/gorm"
)

type Store struct {
	db *gorm.DB
}

var _ directchargecontract.Store = (*Store)(nil)

func New(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) SpecBySKU(skuID uint) (*directchargedomain.Spec, error) {
	return first[directchargedomain.Spec](s.db, "sku_id = ?", skuID)
}

func (s *Store) SpecByID(id uint) (*directchargedomain.Spec, error) {
	return first[directchargedomain.Spec](s.db, "id = ?", id)
}

func (s *Store) CreatePreflight(credential *directchargedomain.Credential, preflight *directchargedomain.Preflight) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(credential).Error; err != nil {
			return err
		}
		preflight.CredentialID = &credential.ID
		return tx.Create(preflight).Error
	})
}

func (s *Store) PreflightByRef(ref string) (*directchargedomain.Preflight, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	return first[directchargedomain.Preflight](s.db, "ref = ?", ref)
}

func (s *Store) ClaimPreflight(id uint, now time.Time) (bool, error) {
	result := s.db.Model(&directchargedomain.Preflight{}).
		Where("id = ? AND status = ? AND expires_at > ?", id, directchargedomain.PreflightOK, now).
		Updates(map[string]interface{}{"status": directchargedomain.PreflightConsumed, "consumed_at": now})
	return result.RowsAffected == 1, result.Error
}

func (s *Store) ReleasePreflight(id uint) error {
	return s.db.Model(&directchargedomain.Preflight{}).
		Where("id = ? AND status = ?", id, directchargedomain.PreflightConsumed).
		Updates(map[string]interface{}{"status": directchargedomain.PreflightOK, "consumed_at": nil}).Error
}

func (s *Store) TaskByPreflight(preflightID uint) (*directchargedomain.Task, error) {
	return first[directchargedomain.Task](s.db, "preflight_id = ?", preflightID)
}

func (s *Store) TaskByParentOrderNo(userID uint, parentOrderNo string) (*directchargedomain.Task, error) {
	return first[directchargedomain.Task](s.db.Where("user_id = ?", userID), "parent_order_no = ?", parentOrderNo)
}

func (s *Store) LockHolder(fingerprint string) (*directchargedomain.Task, error) {
	if fingerprint == "" {
		return nil, nil
	}
	lock, err := first[directchargedomain.AccountLock](s.db, "account_fingerprint = ?", fingerprint)
	if err != nil || lock == nil {
		return nil, err
	}
	task, err := first[directchargedomain.Task](s.db, "id = ?", lock.TaskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		// 锁指向不存在的任务属于数据异常，按占用处理，交给人工排查
		return &directchargedomain.Task{ID: lock.TaskID, Status: directchargedomain.TaskReview}, nil
	}
	return task, nil
}

func (s *Store) CreateTask(task *directchargedomain.Task, credentialExpiresAt time.Time, actor string) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var held int64
		if err := tx.Model(&directchargedomain.AccountLock{}).
			Where("account_fingerprint = ?", task.AccountFingerprint).Count(&held).Error; err != nil {
			return err
		}
		if held > 0 {
			return directchargecontract.ErrAccountBusy
		}
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		if err := tx.Create(&directchargedomain.AccountLock{
			AccountFingerprint: task.AccountFingerprint,
			TaskID:             task.ID,
			CreatedAt:          task.CreatedAt,
		}).Error; err != nil {
			return err
		}
		if task.CredentialID != nil {
			if err := tx.Model(&directchargedomain.Credential{}).
				Where("id = ? AND destroyed_at IS NULL", *task.CredentialID).
				Updates(map[string]interface{}{"task_id": task.ID, "expires_at": credentialExpiresAt}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&directchargedomain.TaskLog{
			TaskID: task.ID, ToStatus: task.Status, Reason: "order created", Actor: actor, CreatedAt: task.CreatedAt,
		}).Error
	})
	if err != nil && !errors.Is(err, directchargecontract.ErrAccountBusy) && isUniqueViolation(err) {
		// 并发下另一任务抢先占用了同一账号
		return directchargecontract.ErrAccountBusy
	}
	return err
}

func (s *Store) CloseUnpaidTask(taskID uint, version int, reason, actor string, now time.Time) (bool, error) {
	closed := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&directchargedomain.Task{}).
			Where("id = ? AND version = ? AND status = ?", taskID, version, directchargedomain.TaskAwaitingPayment).
			Updates(map[string]interface{}{
				"status":     directchargedomain.TaskClosed,
				"version":    version + 1,
				"updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		closed = true
		if err := tx.Where("task_id = ?", taskID).Delete(&directchargedomain.AccountLock{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&directchargedomain.Credential{}).
			Where("task_id = ? AND destroyed_at IS NULL", taskID).
			Updates(map[string]interface{}{"ciphertext": "", "destroyed_at": now}).Error; err != nil {
			return err
		}
		return tx.Create(&directchargedomain.TaskLog{
			TaskID: taskID, FromStatus: directchargedomain.TaskAwaitingPayment, ToStatus: directchargedomain.TaskClosed,
			Reason: reason, Actor: actor, CreatedAt: now,
		}).Error
	})
	return closed, err
}

func (s *Store) TaskLogs(taskID uint) ([]directchargedomain.TaskLog, error) {
	var logs []directchargedomain.TaskLog
	err := s.db.Where("task_id = ?", taskID).Order("created_at ASC, id ASC").Find(&logs).Error
	return logs, err
}

func first[T any](db *gorm.DB, query string, args ...interface{}) (*T, error) {
	var out T
	if err := db.Where(query, args...).First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}
