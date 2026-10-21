package format

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dujiao-next/internal/constants"
	settingsmessaging "github.com/dujiao-next/internal/modules/settings/schema/messaging"
	settingsstorefront "github.com/dujiao-next/internal/modules/settings/schema/storefront"
	"github.com/dujiao-next/internal/queue"
	"github.com/shopspring/decimal"
)

func thresholdValueByAlertType(setting settingsstorefront.DashboardAlertSetting, alertType string) int64 {
	switch alertType {
	case constants.NotificationAlertTypeOutOfStockProducts:
		return setting.OutOfStockProductsThreshold
	case constants.NotificationAlertTypeLowStockProducts:
		return setting.LowStockThreshold
	case constants.NotificationAlertTypePendingOrders:
		return setting.PendingPaymentOrdersThreshold
	case constants.NotificationAlertTypePaymentsFailed:
		return setting.PaymentsFailedThreshold
	default:
		return 0
	}
}

func isInventoryAlertType(alertType string) bool {
	switch strings.ToLower(strings.TrimSpace(alertType)) {
	case constants.NotificationAlertTypeOutOfStockProducts, constants.NotificationAlertTypeLowStockProducts:
		return true
	default:
		return false
	}
}

func alertTypeLabelByType(locale, alertType string) string {
	type labels struct{ zhCN, zhTW, enUS string }
	values := map[string]labels{
		constants.NotificationAlertTypeOutOfStockProducts: {"售罄商品", "售罄商品", "Out of Stock"},
		constants.NotificationAlertTypeLowStockProducts:   {"低库存商品", "低庫存商品", "Low Stock"},
		constants.NotificationAlertTypePendingOrders:      {"待支付订单", "待支付訂單", "Pending Payment"},
		constants.NotificationAlertTypePaymentsFailed:     {"支付失败", "支付失敗", "Payment Failed"},
		constants.NotificationAlertTypeUpstreamBalanceLow: {"ZovoCard 余额不足", "ZovoCard 餘額不足", "ZovoCard balance low"},
		constants.NotificationAlertTypeUpstreamBalanceQueryFailed: {
			"ZovoCard 余额查询失败", "ZovoCard 餘額查詢失敗", "ZovoCard balance query failed",
		},
	}
	value, ok := values[alertType]
	if !ok {
		return alertType
	}
	switch strings.ToLower(strings.TrimSpace(locale)) {
	case "zh-tw":
		return value.zhTW
	case "en-us", "en":
		return value.enUS
	default:
		return value.zhCN
	}
}

func NormalizeInventoryAlertTypeKey(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	inventoryAlertTypes := []string{
		constants.NotificationAlertTypeOutOfStockProducts,
		constants.NotificationAlertTypeLowStockProducts,
	}
	locales := []string{
		constants.LocaleZhCN,
		constants.LocaleZhTW,
		constants.LocaleEnUS,
	}
	for _, alertType := range inventoryAlertTypes {
		if normalized == strings.ToLower(strings.TrimSpace(alertType)) {
			return alertType
		}
		for _, locale := range locales {
			label := strings.ToLower(strings.TrimSpace(alertTypeLabelByType(locale, alertType)))
			if normalized == label {
				return alertType
			}
		}
	}
	return ""
}

func ResolveInventoryAlertTypeKey(data map[string]interface{}) string {
	if len(data) == 0 {
		return ""
	}
	if key := NormalizeInventoryAlertTypeKey(toString(data["alert_type_key"])); key != "" {
		return key
	}
	return NormalizeInventoryAlertTypeKey(toString(data["alert_type"]))
}

func toString(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

// upstreamBalanceQueryErrorMaxRunes 告警里展示的查询错误最大长度
const upstreamBalanceQueryErrorMaxRunes = 200

// UpstreamBalanceAlertInput 描述一项上游余额巡检结果。CardID 为 0 表示账户可消费余额，否则为付款卡。
type UpstreamBalanceAlertInput struct {
	CardID      int64
	Value       decimal.Decimal
	Threshold   decimal.Decimal
	Total       decimal.Decimal
	HasTotal    bool
	Stale       bool
	AsOf        string
	RepeatHours int
	QueryError  string
}

// UpstreamBalanceIsLow 余额严格低于阈值时视为不足；阈值不为正数时不检查。
func UpstreamBalanceIsLow(value, threshold decimal.Decimal) bool {
	return threshold.IsPositive() && value.LessThan(threshold)
}

// MaskUpstreamCardID 只保留卡 ID 末 4 位，如 525944 → ****5944。
func MaskUpstreamCardID(cardID int64) string {
	text := strconv.FormatInt(cardID, 10)
	if len(text) > 4 {
		text = text[len(text)-4:]
	}
	return "****" + text
}

// UpstreamBalanceTargetKey 返回巡检对象的稳定键，用于告警间隔去重。
func UpstreamBalanceTargetKey(cardID int64) string {
	if cardID <= 0 {
		return "account"
	}
	return "card:" + strconv.FormatInt(cardID, 10)
}

// BuildUpstreamBalanceLowDispatchPayload 构建上游余额不足告警。
func BuildUpstreamBalanceLowDispatchPayload(
	setting settingsmessaging.NotificationCenterSetting,
	payload queue.NotificationDispatchPayload,
	input UpstreamBalanceAlertInput,
) queue.NotificationDispatchPayload {
	locale := ResolveLocale(payload.Locale, setting.DefaultLocale)
	alertType := constants.NotificationAlertTypeUpstreamBalanceLow
	target := upstreamBalanceTargetLabel(locale, input.CardID)
	value := formatUpstreamUSD(input.Value)
	threshold := formatUpstreamUSD(input.Threshold)
	repeat := formatUpstreamRepeatHours(locale, input.RepeatHours)

	level := "warning"
	if !input.Value.IsPositive() {
		level = "error"
	}
	message := localizedNotificationText(
		locale,
		fmt.Sprintf("%s为 %s，低于告警阈值 %s，请及时充值。", target, value, threshold),
		fmt.Sprintf("%s為 %s，低於告警門檻 %s，請及時儲值。", target, value, threshold),
		fmt.Sprintf("%s is %s, below the alert threshold of %s. Please top up soon.", target, value, threshold),
	)
	if input.HasTotal {
		total := formatUpstreamUSD(input.Total)
		message += localizedNotificationText(
			locale,
			fmt.Sprintf("账面余额 %s。", total),
			fmt.Sprintf("帳面餘額 %s。", total),
			fmt.Sprintf(" Book balance: %s.", total),
		)
	}
	if input.Stale {
		asOf := strings.TrimSpace(input.AsOf)
		if asOf == "" {
			asOf = "-"
		}
		message += localizedNotificationText(
			locale,
			fmt.Sprintf("注意：上游标记该余额可能不是最新值（余额时间 %s）。", asOf),
			fmt.Sprintf("注意：上游標記該餘額可能不是最新值（餘額時間 %s）。", asOf),
			fmt.Sprintf(" Note: upstream marks this balance as possibly out of date (as of %s).", asOf),
		)
	}
	message += localizedNotificationText(
		locale,
		fmt.Sprintf("余额仍不足时最短 %s 提醒一次。", repeat),
		fmt.Sprintf("餘額仍不足時最短 %s 提醒一次。", repeat),
		fmt.Sprintf(" While still low, this alert repeats at most once every %s.", repeat),
	)

	data := newUpstreamBalanceAlertData(payload, locale, alertType, input.CardID, target)
	data["alert_level"] = level
	data["alert_value"] = value
	data["alert_threshold"] = threshold
	data["upstream_balance_value"] = value
	data["upstream_balance_threshold"] = threshold
	if input.HasTotal {
		data["upstream_balance_total"] = formatUpstreamUSD(input.Total)
	}
	data["upstream_balance_stale"] = strconv.FormatBool(input.Stale)
	data["upstream_balance_as_of"] = strings.TrimSpace(input.AsOf)
	data["message"] = message
	return upstreamBalanceDispatchPayload(payload, data)
}

// BuildUpstreamBalanceQueryFailedDispatchPayload 构建上游余额查询失败告警（不代表余额不足）。
func BuildUpstreamBalanceQueryFailedDispatchPayload(
	setting settingsmessaging.NotificationCenterSetting,
	payload queue.NotificationDispatchPayload,
	input UpstreamBalanceAlertInput,
) queue.NotificationDispatchPayload {
	locale := ResolveLocale(payload.Locale, setting.DefaultLocale)
	alertType := constants.NotificationAlertTypeUpstreamBalanceQueryFailed
	target := upstreamBalanceTargetLabel(locale, input.CardID)
	threshold := formatUpstreamUSD(input.Threshold)
	repeat := formatUpstreamRepeatHours(locale, input.RepeatHours)
	reason := truncateUpstreamQueryError(input.QueryError)
	if reason == "" {
		reason = "-"
	}

	data := newUpstreamBalanceAlertData(payload, locale, alertType, input.CardID, target)
	data["alert_level"] = "error"
	data["alert_value"] = localizedNotificationText(locale, "查询失败", "查詢失敗", "Query failed")
	data["alert_threshold"] = threshold
	data["upstream_balance_threshold"] = threshold
	data["upstream_balance_error"] = reason
	data["message"] = localizedNotificationText(
		locale,
		fmt.Sprintf("无法查询%s（%s）。这不代表余额不足，请检查 ZovoCard 服务状态与 API 配置；问题持续时最短 %s 提醒一次。", target, reason, repeat),
		fmt.Sprintf("無法查詢%s（%s）。這不代表餘額不足，請檢查 ZovoCard 服務狀態與 API 設定；問題持續時最短 %s 提醒一次。", target, reason, repeat),
		fmt.Sprintf("Unable to query %s (%s). This does not mean the balance is low; please check the ZovoCard service and API configuration. While the problem persists, this alert repeats at most once every %s.", target, reason, repeat),
	)
	return upstreamBalanceDispatchPayload(payload, data)
}

func newUpstreamBalanceAlertData(payload queue.NotificationDispatchPayload, locale, alertType string, cardID int64, target string) map[string]interface{} {
	data := CloneVariables(payload.Data)
	data["alert_type"] = alertTypeLabelByType(locale, alertType)
	data["alert_type_label"] = data["alert_type"]
	data["alert_type_key"] = alertType
	data["upstream_provider"] = "ZovoCard"
	data["upstream_balance_scope"] = "account"
	data["upstream_balance_target"] = target
	if cardID > 0 {
		data["upstream_balance_scope"] = "card"
		data["upstream_card_id_masked"] = MaskUpstreamCardID(cardID)
	}
	return data
}

func upstreamBalanceDispatchPayload(payload queue.NotificationDispatchPayload, data map[string]interface{}) queue.NotificationDispatchPayload {
	itemPayload := payload
	itemPayload.EventType = constants.NotificationEventExceptionAlert
	itemPayload.BizType = constants.NotificationBizTypeDashboardAlert
	itemPayload.BizID = 0
	itemPayload.Data = data
	return itemPayload
}

func upstreamBalanceTargetLabel(locale string, cardID int64) string {
	if cardID <= 0 {
		return localizedNotificationText(locale, "ZovoCard 账户可消费余额", "ZovoCard 帳戶可用餘額", "ZovoCard account spendable balance")
	}
	masked := MaskUpstreamCardID(cardID)
	return localizedNotificationText(
		locale,
		"ZovoCard 付款卡 "+masked+" 可用额度",
		"ZovoCard 付款卡 "+masked+" 可用額度",
		"ZovoCard funding card "+masked+" available balance",
	)
}

func formatUpstreamUSD(amount decimal.Decimal) string {
	return amount.Round(2).StringFixed(2) + " USD"
}

func formatUpstreamRepeatHours(locale string, hours int) string {
	hours = settingsmessaging.NormalizeNotificationUpstreamBalanceAlertRepeatHours(hours)
	return localizedNotificationText(locale, fmt.Sprintf("%d 小时", hours), fmt.Sprintf("%d 小時", hours), fmt.Sprintf("%d hours", hours))
}

func truncateUpstreamQueryError(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if utf8.RuneCountInString(message) <= upstreamBalanceQueryErrorMaxRunes {
		return message
	}
	return string([]rune(message)[:upstreamBalanceQueryErrorMaxRunes]) + "…"
}
