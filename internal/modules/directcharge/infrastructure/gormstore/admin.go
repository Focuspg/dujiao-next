package gormstore

import (
	"strings"

	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

func (s *Store) ListSpecs() ([]directchargedomain.Spec, error) {
	var specs []directchargedomain.Spec
	err := s.db.Order("id ASC").Find(&specs).Error
	return specs, err
}

func (s *Store) SaveSpec(spec *directchargedomain.Spec) error {
	var err error
	if spec.ID == 0 {
		err = s.db.Create(spec).Error
	} else {
		err = s.db.Save(spec).Error
	}
	if err != nil && isUniqueViolation(err) {
		return directchargecontract.ErrSpecDuplicate
	}
	return err
}

func (s *Store) TaskByID(id uint) (*directchargedomain.Task, error) {
	return first[directchargedomain.Task](s.db, "id = ?", id)
}

func (s *Store) ListTasks(filter directchargecontract.TaskFilter) ([]directchargedomain.Task, int64, error) {
	query := s.db.Model(&directchargedomain.Task{})
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if refundStatus := strings.TrimSpace(filter.RefundStatus); refundStatus != "" {
		query = query.Where("refund_status = ?", refundStatus)
	}
	if orderNo := strings.TrimSpace(filter.OrderNo); orderNo != "" {
		query = query.Where("parent_order_no = ? OR order_no = ?", orderNo, orderNo)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	var tasks []directchargedomain.Task
	err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks).Error
	return tasks, total, err
}

func (s *Store) AttemptsByTask(taskID uint) ([]directchargedomain.Attempt, error) {
	var attempts []directchargedomain.Attempt
	err := s.db.Where("task_id = ?", taskID).Order("attempt_no ASC").Find(&attempts).Error
	return attempts, err
}

func (s *Store) WebhookEventsByTask(taskID uint) ([]directchargedomain.WebhookEvent, error) {
	var events []directchargedomain.WebhookEvent
	err := s.db.Where("task_id = ?", taskID).Order("id ASC").Limit(200).Find(&events).Error
	return events, err
}
