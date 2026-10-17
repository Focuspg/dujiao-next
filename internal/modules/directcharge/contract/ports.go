package contract

import (
	"context"
	"time"

	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// Store 代充持久化
type Store interface {
	SpecBySKU(skuID uint) (*directchargedomain.Spec, error)
	SpecByID(id uint) (*directchargedomain.Spec, error)
	// CreatePreflight 在一个事务内保存凭据密文与预检记录，并回填 Preflight.CredentialID
	CreatePreflight(credential *directchargedomain.Credential, preflight *directchargedomain.Preflight) error
	PreflightByRef(ref string) (*directchargedomain.Preflight, error)
	// ClaimPreflight 仅当状态仍为 ok 且未过期时标记为 consumed，返回是否抢到
	ClaimPreflight(id uint, now time.Time) (bool, error)
	// ReleasePreflight 下单失败时把未建任务的预检退回 ok
	ReleasePreflight(id uint) error
	TaskByPreflight(preflightID uint) (*directchargedomain.Task, error)
	TaskByParentOrderNo(userID uint, parentOrderNo string) (*directchargedomain.Task, error)
	// LockHolder 返回占用该账号指纹的任务，没有返回 nil
	LockHolder(fingerprint string) (*directchargedomain.Task, error)
	// CreateTask 在一个事务内创建任务、占用账号、绑定凭据并延长凭据有效期、写日志；账号已被占用返回 ErrAccountBusy
	CreateTask(task *directchargedomain.Task, credentialExpiresAt time.Time, actor string) error
	// CloseUnpaidTask 把 awaiting_payment 的任务关闭，释放账号并销毁凭据，返回是否关闭
	CloseUnpaidTask(taskID uint, version int, reason, actor string, now time.Time) (bool, error)
	TaskLogs(taskID uint) ([]directchargedomain.TaskLog, error)

	// 以下供后台任务使用

	// TasksByStatus 按 ID 顺序列出某状态的任务
	TasksByStatus(status string, limit int) ([]directchargedomain.Task, error)
	// TasksDue 列出处于给定状态且已到检查时间（next_check_at 为空或不晚于 now）的任务
	TasksDue(statuses []string, now time.Time, limit int) ([]directchargedomain.Task, error)
	// StaleTasks 列出处于给定状态且 updated_at 早于 before 的任务（进程中断遗留）
	StaleTasks(status string, before time.Time, limit int) ([]directchargedomain.Task, error)
	// CountActive 统计某规格正在提交或处理中的任务数
	CountActive(specID uint) (int64, error)
	// TransitionTask 比较状态与版本后写入变化，返回是否写入
	TransitionTask(change TaskChange) (bool, error)
	AttemptByID(id uint) (*directchargedomain.Attempt, error)
	AttemptByClientRequestID(clientRequestID string) (*directchargedomain.Attempt, error)
	AttemptByUpstreamOrderID(upstreamOrderID int64) (*directchargedomain.Attempt, error)
	CredentialByID(id uint) (*directchargedomain.Credential, error)
	PreflightByID(id uint) (*directchargedomain.Preflight, error)
	// ReplaceCredentialSecret 重新预检后换入新的凭据密文（含新预检令牌），并记录新令牌失效时间
	ReplaceCredentialSecret(credentialID uint, ciphertext string, keyVersion int, preflightID uint, tokenExpiresAt *time.Time) error
	// PaidOrdersWithoutTask 已付款但没有充值任务的代充子订单
	PaidOrdersWithoutTask(limit int) ([]OrphanOrder, error)
	// SaveWebhookEvent 按 (source, dedup_key) 去重保存回调，返回是否为新事件
	SaveWebhookEvent(event *directchargedomain.WebhookEvent) (bool, error)
	MarkWebhookEvent(id uint, status string, taskID *uint, now time.Time) error
	UnmatchedWebhookEvents(since time.Time, limit int) ([]directchargedomain.WebhookEvent, error)
	// WakeTask 让进行中的任务在下一轮立即查单
	WakeTask(taskID uint, now time.Time) error
}

// Upstream 供应商预检
type Upstream interface {
	Preflight(ctx context.Context, request UpstreamPreflightRequest) (*UpstreamPreflight, error)
	// CreateOrder 提交直充；失败返回 *UpstreamError
	CreateOrder(ctx context.Context, request UpstreamOrderRequest) (*UpstreamOrder, error)
	// GetOrder 查询上游订单；失败返回 *UpstreamError
	GetOrder(ctx context.Context, upstreamOrderID int64) (*UpstreamOrder, error)
	// FindOrder 在最近的订单列表里按请求号查找（兜底，列表不支持按请求号筛选）；没找到返回 nil
	FindOrder(ctx context.Context, clientRequestID string, maxPages int) (*UpstreamOrder, error)
	// ParseWebhook 用原始请求体校验签名并解析回调；签名无效返回 ErrWebhookSignature
	ParseWebhook(rawBody []byte, signature string) (*UpstreamEvent, error)
}

// Vault 凭据加密与账号指纹
type Vault interface {
	SealCredential(mode, credential, preflightToken string) (ciphertext string, keyVersion int, err error)
	Fingerprint(product, accountID string) string
	Mask(accountID string) string
	OpenCredential(ciphertext string, keyVersion int) (OpenedCredential, error)
}

// Orders 本地订单
type Orders interface {
	CreateDirectChargeOrder(request OrderRequest) (*OrderRef, error)
	CancelUnpaid(parentOrderID, userID uint) error
	// Status 返回子订单当前状态；订单不存在返回空字符串
	Status(orderID uint) (string, error)
	// Deliver 充值确认成功后交付子订单（通知用户）；已交付时视为成功
	Deliver(orderID uint, payload string) error
}

// Alerter 站长告警；未配置时只写日志
type Alerter interface {
	Alert(alert Alert) error
}
