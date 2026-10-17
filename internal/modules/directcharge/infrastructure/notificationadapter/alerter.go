// Package notificationadapter 把代充告警投递到通知中心的“异常告警”场景（站长邮件）。
package notificationadapter

import (
	"time"

	"github.com/dujiao-next/internal/constants"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	notificationcontract "github.com/dujiao-next/internal/modules/notification/contract"
	"github.com/dujiao-next/internal/shared/jsonmap"
)

type Alerter struct {
	enqueuer notificationcontract.NotificationEnqueuer
}

var _ directchargecontract.Alerter = (*Alerter)(nil)

// New 通知中心未装配时返回 nil，用例只写日志
func New(enqueuer notificationcontract.NotificationEnqueuer) directchargecontract.Alerter {
	if enqueuer == nil {
		return nil
	}
	return &Alerter{enqueuer: enqueuer}
}

func (a *Alerter) Alert(alert directchargecontract.Alert) error {
	return a.enqueuer.Enqueue(notificationcontract.EnqueueInput{
		EventType: constants.NotificationEventExceptionAlert,
		BizType:   constants.NotificationBizTypeDirectCharge,
		BizID:     alert.TaskID,
		Data: jsonmap.JSON{
			"source":      constants.NotificationBizTypeDirectCharge,
			"alert_type":  alert.Type,
			"alert_level": alert.Level,
			"task_id":     alert.TaskID,
			"order_no":    alert.OrderNo,
			"message":     alert.Message,
			"occurred_at": time.Now().Format("2006-01-02 15:04:05"),
		},
	})
}
