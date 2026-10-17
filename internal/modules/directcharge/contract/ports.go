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
}

// Upstream 供应商预检
type Upstream interface {
	Preflight(ctx context.Context, request UpstreamPreflightRequest) (*UpstreamPreflight, error)
}

// Vault 凭据加密与账号指纹
type Vault interface {
	SealCredential(mode, credential, preflightToken string) (ciphertext string, keyVersion int, err error)
	Fingerprint(product, accountID string) string
	Mask(accountID string) string
}

// Orders 本地订单
type Orders interface {
	CreateDirectChargeOrder(request OrderRequest) (*OrderRef, error)
	CancelUnpaid(parentOrderID, userID uint) error
	// Status 返回子订单当前状态；订单不存在返回空字符串
	Status(orderID uint) (string, error)
}
