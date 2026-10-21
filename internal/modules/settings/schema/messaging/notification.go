package settingsmessaging

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	"github.com/dujiao-next/internal/constants"
	settingsvalue "github.com/dujiao-next/internal/modules/settings/schema/value"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/shopspring/decimal"
)

var ErrNotificationConfigInvalid = errors.New("notification config invalid")

var notificationSupportedLocales = map[string]struct{}{
	constants.LocaleZhCN: {},
	constants.LocaleZhTW: {},
	constants.LocaleEnUS: {},
}

var telegramChatIDPattern = regexp.MustCompile(`^-?\d{5,20}$`)

const (
	FeishuReceiveIDTypeChatID  = "chat_id"
	FeishuReceiveIDTypeOpenID  = "open_id"
	FeishuReceiveIDTypeUserID  = "user_id"
	FeishuReceiveIDTypeUnionID = "union_id"
	FeishuReceiveIDTypeEmail   = "email"
)

var feishuSupportedReceiveIDTypes = map[string]struct{}{
	FeishuReceiveIDTypeChatID:  {},
	FeishuReceiveIDTypeOpenID:  {},
	FeishuReceiveIDTypeUserID:  {},
	FeishuReceiveIDTypeUnionID: {},
	FeishuReceiveIDTypeEmail:   {},
}

const (
	notificationInventoryAlertIntervalDefaultSeconds = 1800
	notificationInventoryAlertIntervalMinSeconds     = 60
	notificationInventoryAlertIntervalMaxSeconds     = 604800

	notificationPaymentOrderAlertIntervalDefaultSeconds = 1800
	notificationPaymentOrderAlertIntervalMinSeconds     = 60
	notificationPaymentOrderAlertIntervalMaxSeconds     = 604800
	notificationPaymentOrderAlertCheckDefaultSeconds    = 86400
	notificationPaymentOrderAlertCheckMinSeconds        = 60
	notificationPaymentOrderAlertCheckMaxSeconds        = 604800

	notificationUpstreamBalanceAlertCheckDefaultMinutes  = 30
	notificationUpstreamBalanceAlertCheckMinMinutes      = 5
	notificationUpstreamBalanceAlertCheckMaxMinutes      = 1440
	notificationUpstreamBalanceAlertRepeatDefaultHours   = 6
	notificationUpstreamBalanceAlertRepeatMinHours       = 1
	notificationUpstreamBalanceAlertRepeatMaxHours       = 168
	notificationUpstreamBalanceAlertThresholdMaxUSDValue = 1000000
)

// NotificationChannelSetting 通知渠道配置
type NotificationChannelSetting struct {
	Enabled    bool     `json:"enabled"`
	Recipients []string `json:"recipients"`
}

// FeishuNotificationChannelSetting 飞书自建应用机器人通知配置。
type FeishuNotificationChannelSetting struct {
	Enabled       bool     `json:"enabled"`
	AppID         string   `json:"app_id"`
	AppSecret     string   `json:"app_secret"`
	ReceiveIDType string   `json:"receive_id_type"`
	Recipients    []string `json:"recipients"`
}

// NotificationChannelsSetting 通知渠道集合
type NotificationChannelsSetting struct {
	Email    NotificationChannelSetting       `json:"email"`
	Telegram NotificationChannelSetting       `json:"telegram"`
	Feishu   FeishuNotificationChannelSetting `json:"feishu"`
}

// NotificationSceneSetting 通知场景开关
type NotificationSceneSetting struct {
	WalletRechargeSuccess    bool `json:"wallet_recharge_success"`
	OrderPaidSuccess         bool `json:"order_paid_success"`
	ManualFulfillmentPending bool `json:"manual_fulfillment_pending"`
	ExceptionAlert           bool `json:"exception_alert"`
}

// NotificationLocalizedTemplate 通知多语言模板
type NotificationLocalizedTemplate struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// NotificationSceneTemplate 单个通知场景模板
type NotificationSceneTemplate struct {
	ZHCN NotificationLocalizedTemplate `json:"zh-CN"`
	ZHTW NotificationLocalizedTemplate `json:"zh-TW"`
	ENUS NotificationLocalizedTemplate `json:"en-US"`
}

// NotificationTemplatesSetting 通知模板集合
type NotificationTemplatesSetting struct {
	WalletRechargeSuccess    NotificationSceneTemplate `json:"wallet_recharge_success"`
	OrderPaidSuccess         NotificationSceneTemplate `json:"order_paid_success"`
	ManualFulfillmentPending NotificationSceneTemplate `json:"manual_fulfillment_pending"`
	ExceptionAlert           NotificationSceneTemplate `json:"exception_alert"`
}

// NotificationCenterSetting 通知中心配置
type NotificationCenterSetting struct {
	DefaultLocale                    string                       `json:"default_locale"`
	Channels                         NotificationChannelsSetting  `json:"channels"`
	Scenes                           NotificationSceneSetting     `json:"scenes"`
	Templates                        NotificationTemplatesSetting `json:"templates"`
	DedupeTTLSeconds                 int                          `json:"dedupe_ttl_seconds"`
	InventoryAlertIntervalSeconds    int                          `json:"inventory_alert_interval_seconds"`
	PaymentOrderAlertIntervalSeconds int                          `json:"payment_order_alert_interval_seconds"`
	PaymentOrderAlertCheckSeconds    int                          `json:"payment_order_alert_check_interval_seconds"`
	IgnoredProductIDs                []uint                       `json:"ignored_product_ids"`
	// UpstreamBalanceAlertEnabled 是否巡检上游 ZovoCard 余额
	UpstreamBalanceAlertEnabled bool `json:"upstream_balance_alert_enabled"`
	// UpstreamBalanceAlertThresholdUSD 账户可消费余额告警阈值（美元，两位小数字符串）；空表示不检查
	UpstreamBalanceAlertThresholdUSD string `json:"upstream_balance_alert_threshold_usd"`
	// UpstreamCardBalanceAlertThresholdUSD 付款卡可用额度告警阈值（美元，两位小数字符串）；空表示不检查
	UpstreamCardBalanceAlertThresholdUSD string `json:"upstream_card_balance_alert_threshold_usd"`
	// UpstreamBalanceAlertCheckMinutes 查询上游余额的最小间隔（分钟）
	UpstreamBalanceAlertCheckMinutes int `json:"upstream_balance_alert_check_interval_minutes"`
	// UpstreamBalanceAlertRepeatHours 同一项余额持续不足（或持续查询失败）时的重复提醒间隔（小时）
	UpstreamBalanceAlertRepeatHours int `json:"upstream_balance_alert_repeat_interval_hours"`
}

// NotificationCenterSettingPatch 通知中心配置补丁
type NotificationCenterSettingPatch struct {
	DefaultLocale                    *string                     `json:"default_locale"`
	Channels                         *NotificationChannelsPatch  `json:"channels"`
	Scenes                           *NotificationScenePatch     `json:"scenes"`
	Templates                        *NotificationTemplatesPatch `json:"templates"`
	DedupeTTLSeconds                 *int                        `json:"dedupe_ttl_seconds"`
	InventoryAlertIntervalSeconds    *int                        `json:"inventory_alert_interval_seconds"`
	PaymentOrderAlertIntervalSeconds *int                        `json:"payment_order_alert_interval_seconds"`
	PaymentOrderAlertCheckSeconds    *int                        `json:"payment_order_alert_check_interval_seconds"`
	IgnoredProductIDs                *[]uint                     `json:"ignored_product_ids"`
	UpstreamBalanceAlertEnabled      *bool                       `json:"upstream_balance_alert_enabled"`
	// UpstreamBalanceAlertThresholdUSD 金额字符串，如 "5.00"；空字符串表示不检查
	UpstreamBalanceAlertThresholdUSD     *string `json:"upstream_balance_alert_threshold_usd"`
	UpstreamCardBalanceAlertThresholdUSD *string `json:"upstream_card_balance_alert_threshold_usd"`
	UpstreamBalanceAlertCheckMinutes     *int    `json:"upstream_balance_alert_check_interval_minutes"`
	UpstreamBalanceAlertRepeatHours      *int    `json:"upstream_balance_alert_repeat_interval_hours"`
}

// NotificationChannelsPatch 通知渠道补丁
type NotificationChannelsPatch struct {
	Email    *NotificationChannelPatch       `json:"email"`
	Telegram *NotificationChannelPatch       `json:"telegram"`
	Feishu   *FeishuNotificationChannelPatch `json:"feishu"`
}

// NotificationChannelPatch 通知渠道补丁
type NotificationChannelPatch struct {
	Enabled    *bool     `json:"enabled"`
	Recipients *[]string `json:"recipients"`
}

// FeishuNotificationChannelPatch 飞书机器人通知配置补丁。
type FeishuNotificationChannelPatch struct {
	Enabled       *bool     `json:"enabled"`
	AppID         *string   `json:"app_id"`
	AppSecret     *string   `json:"app_secret"`
	ReceiveIDType *string   `json:"receive_id_type"`
	Recipients    *[]string `json:"recipients"`
}

// NotificationScenePatch 通知场景补丁
type NotificationScenePatch struct {
	WalletRechargeSuccess    *bool `json:"wallet_recharge_success"`
	OrderPaidSuccess         *bool `json:"order_paid_success"`
	ManualFulfillmentPending *bool `json:"manual_fulfillment_pending"`
	ExceptionAlert           *bool `json:"exception_alert"`
}

// NotificationTemplatesPatch 通知模板补丁
type NotificationTemplatesPatch struct {
	WalletRechargeSuccess    *NotificationSceneTemplatePatch `json:"wallet_recharge_success"`
	OrderPaidSuccess         *NotificationSceneTemplatePatch `json:"order_paid_success"`
	ManualFulfillmentPending *NotificationSceneTemplatePatch `json:"manual_fulfillment_pending"`
	ExceptionAlert           *NotificationSceneTemplatePatch `json:"exception_alert"`
}

// NotificationSceneTemplatePatch 单场景模板补丁
type NotificationSceneTemplatePatch struct {
	ZHCN *NotificationLocalizedTemplatePatch `json:"zh-CN"`
	ZHTW *NotificationLocalizedTemplatePatch `json:"zh-TW"`
	ENUS *NotificationLocalizedTemplatePatch `json:"en-US"`
}

// NotificationLocalizedTemplatePatch 多语言模板补丁
type NotificationLocalizedTemplatePatch struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
}

// NotificationCenterDefaultSetting 默认通知中心配置
func NotificationCenterDefaultSetting() NotificationCenterSetting {
	return NormalizeNotificationCenterSetting(NotificationCenterSetting{
		DefaultLocale: constants.LocaleZhCN,
		Channels: NotificationChannelsSetting{
			Email: NotificationChannelSetting{
				Enabled:    false,
				Recipients: []string{},
			},
			Telegram: NotificationChannelSetting{
				Enabled:    false,
				Recipients: []string{},
			},
			Feishu: FeishuNotificationChannelSetting{
				Enabled:       false,
				ReceiveIDType: FeishuReceiveIDTypeChatID,
				Recipients:    []string{},
			},
		},
		Scenes: NotificationSceneSetting{
			WalletRechargeSuccess:    true,
			OrderPaidSuccess:         true,
			ManualFulfillmentPending: true,
			ExceptionAlert:           true,
		},
		Templates: NotificationTemplatesSetting{
			WalletRechargeSuccess: NotificationSceneTemplate{
				ZHCN: NotificationLocalizedTemplate{
					Title: "用户充值成功通知",
					Body:  "用户：{{customer_label}}\n邮箱：{{customer_email}}\n充值单号：{{recharge_no}}\n充值金额：{{amount}} {{currency}}\n支付渠道：{{payment_channel}}",
				},
				ZHTW: NotificationLocalizedTemplate{
					Title: "用戶儲值成功通知",
					Body:  "用戶：{{customer_label}}\n郵箱：{{customer_email}}\n儲值單號：{{recharge_no}}\n儲值金額：{{amount}} {{currency}}\n支付渠道：{{payment_channel}}",
				},
				ENUS: NotificationLocalizedTemplate{
					Title: "Wallet Recharge Succeeded",
					Body:  "Customer: {{customer_label}}\nEmail: {{customer_email}}\nRecharge No: {{recharge_no}}\nAmount: {{amount}} {{currency}}\nChannel: {{payment_channel}}",
				},
			},
			OrderPaidSuccess: NotificationSceneTemplate{
				ZHCN: NotificationLocalizedTemplate{
					Title: "订单支付成功通知",
					Body:  "购买人：{{customer_label}}\n邮箱：{{customer_email}}\n订单号：{{order_no}}\n订单金额：{{amount}} {{currency}}\n支付渠道：{{payment_channel}}\n商品明细：\n{{items_summary}}\n交付摘要：{{delivery_summary}}",
				},
				ZHTW: NotificationLocalizedTemplate{
					Title: "訂單支付成功通知",
					Body:  "購買人：{{customer_label}}\n郵箱：{{customer_email}}\n訂單號：{{order_no}}\n訂單金額：{{amount}} {{currency}}\n支付渠道：{{payment_channel}}\n商品明細：\n{{items_summary}}\n交付摘要：{{delivery_summary}}",
				},
				ENUS: NotificationLocalizedTemplate{
					Title: "Order Payment Succeeded",
					Body:  "Customer: {{customer_label}}\nEmail: {{customer_email}}\nOrder No: {{order_no}}\nAmount: {{amount}} {{currency}}\nChannel: {{payment_channel}}\nItems:\n{{items_summary}}\nDelivery Summary: {{delivery_summary}}",
				},
			},
			ManualFulfillmentPending: NotificationSceneTemplate{
				ZHCN: NotificationLocalizedTemplate{
					Title: "待人工交付订单提醒",
					Body:  "购买人：{{customer_label}}\n邮箱：{{customer_email}}\n订单号：{{order_no}}\n订单状态：{{order_status}}\n待处理商品：\n{{fulfillment_items_summary}}\n交付摘要：{{delivery_summary}}",
				},
				ZHTW: NotificationLocalizedTemplate{
					Title: "待人工交付訂單提醒",
					Body:  "購買人：{{customer_label}}\n郵箱：{{customer_email}}\n訂單號：{{order_no}}\n訂單狀態：{{order_status}}\n待處理商品：\n{{fulfillment_items_summary}}\n交付摘要：{{delivery_summary}}",
				},
				ENUS: NotificationLocalizedTemplate{
					Title: "Manual Fulfillment Required",
					Body:  "Customer: {{customer_label}}\nEmail: {{customer_email}}\nOrder No: {{order_no}}\nOrder Status: {{order_status}}\nPending Items:\n{{fulfillment_items_summary}}\nDelivery Summary: {{delivery_summary}}",
				},
			},
			ExceptionAlert: NotificationSceneTemplate{
				ZHCN: NotificationLocalizedTemplate{
					Title: "系统异常告警",
					Body:  "告警类型：{{alert_type}}\n告警级别：{{alert_level}}\n当前值：{{alert_value}}\n阈值：{{alert_threshold}}\n详情：{{message}}\n{{affected_items_summary}}",
				},
				ZHTW: NotificationLocalizedTemplate{
					Title: "系統異常告警",
					Body:  "告警類型：{{alert_type}}\n告警級別：{{alert_level}}\n當前值：{{alert_value}}\n閾值：{{alert_threshold}}\n詳情：{{message}}\n{{affected_items_summary}}",
				},
				ENUS: NotificationLocalizedTemplate{
					Title: "System Exception Alert",
					Body:  "Type: {{alert_type}}\nLevel: {{alert_level}}\nCurrent: {{alert_value}}\nThreshold: {{alert_threshold}}\nDetails: {{message}}\n{{affected_items_summary}}",
				},
			},
		},
		DedupeTTLSeconds:                 300,
		InventoryAlertIntervalSeconds:    notificationInventoryAlertIntervalDefaultSeconds,
		PaymentOrderAlertIntervalSeconds: notificationPaymentOrderAlertIntervalDefaultSeconds,
		PaymentOrderAlertCheckSeconds:    notificationPaymentOrderAlertCheckDefaultSeconds,
		IgnoredProductIDs:                []uint{},
		UpstreamBalanceAlertCheckMinutes: notificationUpstreamBalanceAlertCheckDefaultMinutes,
		UpstreamBalanceAlertRepeatHours:  notificationUpstreamBalanceAlertRepeatDefaultHours,
	})
}

// NormalizeNotificationCenterSetting 归一化通知中心配置
func NormalizeNotificationCenterSetting(setting NotificationCenterSetting) NotificationCenterSetting {
	setting.DefaultLocale = NormalizeNotificationLocale(setting.DefaultLocale)
	setting.Channels.Email.Recipients = normalizeEmailRecipients(setting.Channels.Email.Recipients)
	setting.Channels.Telegram.Recipients = normalizeTelegramRecipients(setting.Channels.Telegram.Recipients)
	setting.Channels.Feishu.AppID = strings.TrimSpace(setting.Channels.Feishu.AppID)
	setting.Channels.Feishu.AppSecret = strings.TrimSpace(setting.Channels.Feishu.AppSecret)
	setting.Channels.Feishu.ReceiveIDType = normalizeFeishuReceiveIDType(setting.Channels.Feishu.ReceiveIDType)
	setting.Channels.Feishu.Recipients = normalizeFeishuRecipients(setting.Channels.Feishu.Recipients, setting.Channels.Feishu.ReceiveIDType)
	setting.DedupeTTLSeconds = normalizeNotificationDedupeTTL(setting.DedupeTTLSeconds)
	setting.InventoryAlertIntervalSeconds = NormalizeNotificationInventoryAlertInterval(setting.InventoryAlertIntervalSeconds)
	setting.PaymentOrderAlertIntervalSeconds = NormalizeNotificationPaymentOrderAlertInterval(setting.PaymentOrderAlertIntervalSeconds)
	setting.PaymentOrderAlertCheckSeconds = NormalizeNotificationPaymentOrderAlertCheck(setting.PaymentOrderAlertCheckSeconds)
	setting.IgnoredProductIDs = normalizeNotificationIgnoredProductIDs(setting.IgnoredProductIDs)
	setting.UpstreamBalanceAlertThresholdUSD = NormalizeNotificationUpstreamBalanceThreshold(setting.UpstreamBalanceAlertThresholdUSD)
	setting.UpstreamCardBalanceAlertThresholdUSD = NormalizeNotificationUpstreamBalanceThreshold(setting.UpstreamCardBalanceAlertThresholdUSD)
	setting.UpstreamBalanceAlertCheckMinutes = NormalizeNotificationUpstreamBalanceAlertCheckMinutes(setting.UpstreamBalanceAlertCheckMinutes)
	setting.UpstreamBalanceAlertRepeatHours = NormalizeNotificationUpstreamBalanceAlertRepeatHours(setting.UpstreamBalanceAlertRepeatHours)
	setting.Templates = normalizeNotificationTemplates(setting.Templates)
	return setting
}

// ValidateNotificationCenterSetting 校验通知中心配置
func ValidateNotificationCenterSetting(setting NotificationCenterSetting) error {
	normalized := NormalizeNotificationCenterSetting(setting)
	if _, ok := notificationSupportedLocales[normalized.DefaultLocale]; !ok {
		return fmt.Errorf("%w: 默认语言不合法", ErrNotificationConfigInvalid)
	}

	if normalized.Channels.Email.Enabled && len(normalized.Channels.Email.Recipients) == 0 {
		return fmt.Errorf("%w: 邮件渠道已启用但未配置收件邮箱", ErrNotificationConfigInvalid)
	}
	if normalized.Channels.Telegram.Enabled && len(normalized.Channels.Telegram.Recipients) == 0 {
		return fmt.Errorf("%w: Telegram 渠道已启用但未配置接收人ID", ErrNotificationConfigInvalid)
	}
	if normalized.Channels.Feishu.Enabled && len(normalized.Channels.Feishu.Recipients) == 0 {
		return fmt.Errorf("%w: 飞书渠道已启用但未配置接收人ID", ErrNotificationConfigInvalid)
	}
	if normalized.Channels.Email.Enabled {
		for _, recipient := range normalized.Channels.Email.Recipients {
			if _, err := mail.ParseAddress(recipient); err != nil {
				return fmt.Errorf("%w: 邮件收件人格式不合法", ErrNotificationConfigInvalid)
			}
		}
	}
	if normalized.Channels.Telegram.Enabled {
		for _, recipient := range normalized.Channels.Telegram.Recipients {
			if !telegramChatIDPattern.MatchString(recipient) {
				return fmt.Errorf("%w: Telegram 接收人ID格式不合法", ErrNotificationConfigInvalid)
			}
		}
	}
	if normalized.Channels.Feishu.Enabled {
		if normalized.Channels.Feishu.AppID == "" {
			return fmt.Errorf("%w: 飞书 App ID 不能为空", ErrNotificationConfigInvalid)
		}
		if normalized.Channels.Feishu.AppSecret == "" {
			return fmt.Errorf("%w: 飞书 App Secret 不能为空", ErrNotificationConfigInvalid)
		}
		if _, ok := feishuSupportedReceiveIDTypes[normalized.Channels.Feishu.ReceiveIDType]; !ok {
			return fmt.Errorf("%w: 飞书接收人ID类型不合法", ErrNotificationConfigInvalid)
		}
		if normalized.Channels.Feishu.ReceiveIDType == FeishuReceiveIDTypeEmail {
			for _, recipient := range normalized.Channels.Feishu.Recipients {
				if _, err := mail.ParseAddress(recipient); err != nil {
					return fmt.Errorf("%w: 飞书接收邮箱格式不合法", ErrNotificationConfigInvalid)
				}
			}
		}
	}

	if normalized.DedupeTTLSeconds < 30 || normalized.DedupeTTLSeconds > 86400 {
		return fmt.Errorf("%w: 去重时长需在 30-86400 秒之间", ErrNotificationConfigInvalid)
	}
	if normalized.InventoryAlertIntervalSeconds < notificationInventoryAlertIntervalMinSeconds || normalized.InventoryAlertIntervalSeconds > notificationInventoryAlertIntervalMaxSeconds {
		return fmt.Errorf("%w: 库存告警频率需在 60-604800 秒之间", ErrNotificationConfigInvalid)
	}
	if normalized.PaymentOrderAlertIntervalSeconds < notificationPaymentOrderAlertIntervalMinSeconds || normalized.PaymentOrderAlertIntervalSeconds > notificationPaymentOrderAlertIntervalMaxSeconds {
		return fmt.Errorf("%w: 支付订单告警频率需在 60-604800 秒之间", ErrNotificationConfigInvalid)
	}
	if normalized.PaymentOrderAlertCheckSeconds < notificationPaymentOrderAlertCheckMinSeconds || normalized.PaymentOrderAlertCheckSeconds > notificationPaymentOrderAlertCheckMaxSeconds {
		return fmt.Errorf("%w: 支付订单告警检查区间需在 60-604800 秒之间", ErrNotificationConfigInvalid)
	}
	return nil
}

// NotificationCenterSettingToMap 将通知中心配置转为 settings 存储结构
func NotificationCenterSettingToMap(setting NotificationCenterSetting) map[string]interface{} {
	normalized := NormalizeNotificationCenterSetting(setting)
	return map[string]interface{}{
		"default_locale": normalized.DefaultLocale,
		"channels": map[string]interface{}{
			"email": map[string]interface{}{
				"enabled":    normalized.Channels.Email.Enabled,
				"recipients": settingsvalue.CloneStringSlice(normalized.Channels.Email.Recipients),
			},
			"telegram": map[string]interface{}{
				"enabled":    normalized.Channels.Telegram.Enabled,
				"recipients": settingsvalue.CloneStringSlice(normalized.Channels.Telegram.Recipients),
			},
			"feishu": map[string]interface{}{
				"enabled":         normalized.Channels.Feishu.Enabled,
				"app_id":          normalized.Channels.Feishu.AppID,
				"app_secret":      normalized.Channels.Feishu.AppSecret,
				"receive_id_type": normalized.Channels.Feishu.ReceiveIDType,
				"recipients":      settingsvalue.CloneStringSlice(normalized.Channels.Feishu.Recipients),
			},
		},
		"scenes": map[string]interface{}{
			"wallet_recharge_success":    normalized.Scenes.WalletRechargeSuccess,
			"order_paid_success":         normalized.Scenes.OrderPaidSuccess,
			"manual_fulfillment_pending": normalized.Scenes.ManualFulfillmentPending,
			"exception_alert":            normalized.Scenes.ExceptionAlert,
		},
		"templates": map[string]interface{}{
			"wallet_recharge_success":    notificationSceneTemplateToMap(normalized.Templates.WalletRechargeSuccess),
			"order_paid_success":         notificationSceneTemplateToMap(normalized.Templates.OrderPaidSuccess),
			"manual_fulfillment_pending": notificationSceneTemplateToMap(normalized.Templates.ManualFulfillmentPending),
			"exception_alert":            notificationSceneTemplateToMap(normalized.Templates.ExceptionAlert),
		},
		"dedupe_ttl_seconds":                            normalized.DedupeTTLSeconds,
		"inventory_alert_interval_seconds":              normalized.InventoryAlertIntervalSeconds,
		"payment_order_alert_interval_seconds":          normalized.PaymentOrderAlertIntervalSeconds,
		"payment_order_alert_check_interval_seconds":    normalized.PaymentOrderAlertCheckSeconds,
		"ignored_product_ids":                           settingsvalue.CloneUintSlice(normalized.IgnoredProductIDs),
		"upstream_balance_alert_enabled":                normalized.UpstreamBalanceAlertEnabled,
		"upstream_balance_alert_threshold_usd":          normalized.UpstreamBalanceAlertThresholdUSD,
		"upstream_card_balance_alert_threshold_usd":     normalized.UpstreamCardBalanceAlertThresholdUSD,
		"upstream_balance_alert_check_interval_minutes": normalized.UpstreamBalanceAlertCheckMinutes,
		"upstream_balance_alert_repeat_interval_hours":  normalized.UpstreamBalanceAlertRepeatHours,
	}
}

// MaskNotificationCenterSettingForAdmin 返回管理端可用配置
func MaskNotificationCenterSettingForAdmin(setting NotificationCenterSetting) jsonmap.JSON {
	normalized := NormalizeNotificationCenterSetting(setting)
	result := jsonmap.JSON(NotificationCenterSettingToMap(normalized))
	channels := settingsvalue.ToStringAnyMap(result["channels"])
	if channels == nil {
		return result
	}
	feishu := settingsvalue.ToStringAnyMap(channels["feishu"])
	if feishu == nil {
		return result
	}
	feishu["app_secret"] = ""
	feishu["has_app_secret"] = normalized.Channels.Feishu.AppSecret != ""
	return result
}

// ApplyNotificationCenterSettingPatch 把补丁应用到当前通知中心配置并完成校验。
func ApplyNotificationCenterSettingPatch(current NotificationCenterSetting, patch NotificationCenterSettingPatch) (NotificationCenterSetting, error) {
	next := current
	if patch.DefaultLocale != nil {
		next.DefaultLocale = strings.TrimSpace(*patch.DefaultLocale)
	}
	if patch.DedupeTTLSeconds != nil {
		next.DedupeTTLSeconds = *patch.DedupeTTLSeconds
	}
	if patch.InventoryAlertIntervalSeconds != nil {
		next.InventoryAlertIntervalSeconds = *patch.InventoryAlertIntervalSeconds
	}
	if patch.PaymentOrderAlertIntervalSeconds != nil {
		next.PaymentOrderAlertIntervalSeconds = *patch.PaymentOrderAlertIntervalSeconds
	}
	if patch.PaymentOrderAlertCheckSeconds != nil {
		next.PaymentOrderAlertCheckSeconds = *patch.PaymentOrderAlertCheckSeconds
	}
	if patch.IgnoredProductIDs != nil {
		next.IgnoredProductIDs = settingsvalue.CloneUintSlice(*patch.IgnoredProductIDs)
	}
	if patch.UpstreamBalanceAlertEnabled != nil {
		next.UpstreamBalanceAlertEnabled = *patch.UpstreamBalanceAlertEnabled
	}
	if patch.UpstreamBalanceAlertThresholdUSD != nil {
		threshold, err := parseNotificationUpstreamBalanceThreshold(*patch.UpstreamBalanceAlertThresholdUSD)
		if err != nil {
			return NotificationCenterSetting{}, err
		}
		next.UpstreamBalanceAlertThresholdUSD = threshold
	}
	if patch.UpstreamCardBalanceAlertThresholdUSD != nil {
		threshold, err := parseNotificationUpstreamBalanceThreshold(*patch.UpstreamCardBalanceAlertThresholdUSD)
		if err != nil {
			return NotificationCenterSetting{}, err
		}
		next.UpstreamCardBalanceAlertThresholdUSD = threshold
	}
	if patch.UpstreamBalanceAlertCheckMinutes != nil {
		next.UpstreamBalanceAlertCheckMinutes = *patch.UpstreamBalanceAlertCheckMinutes
	}
	if patch.UpstreamBalanceAlertRepeatHours != nil {
		next.UpstreamBalanceAlertRepeatHours = *patch.UpstreamBalanceAlertRepeatHours
	}
	if patch.Channels != nil {
		if patch.Channels.Email != nil {
			if patch.Channels.Email.Enabled != nil {
				next.Channels.Email.Enabled = *patch.Channels.Email.Enabled
			}
			if patch.Channels.Email.Recipients != nil {
				next.Channels.Email.Recipients = settingsvalue.CloneStringSlice(*patch.Channels.Email.Recipients)
			}
		}
		if patch.Channels.Telegram != nil {
			if patch.Channels.Telegram.Enabled != nil {
				next.Channels.Telegram.Enabled = *patch.Channels.Telegram.Enabled
			}
			if patch.Channels.Telegram.Recipients != nil {
				next.Channels.Telegram.Recipients = settingsvalue.CloneStringSlice(*patch.Channels.Telegram.Recipients)
			}
		}
		if patch.Channels.Feishu != nil {
			if patch.Channels.Feishu.Enabled != nil {
				next.Channels.Feishu.Enabled = *patch.Channels.Feishu.Enabled
			}
			if patch.Channels.Feishu.AppID != nil {
				next.Channels.Feishu.AppID = strings.TrimSpace(*patch.Channels.Feishu.AppID)
			}
			if patch.Channels.Feishu.AppSecret != nil {
				if appSecret := strings.TrimSpace(*patch.Channels.Feishu.AppSecret); appSecret != "" {
					next.Channels.Feishu.AppSecret = appSecret
				}
			}
			if patch.Channels.Feishu.ReceiveIDType != nil {
				next.Channels.Feishu.ReceiveIDType = strings.ToLower(strings.TrimSpace(*patch.Channels.Feishu.ReceiveIDType))
			}
			if patch.Channels.Feishu.Recipients != nil {
				next.Channels.Feishu.Recipients = settingsvalue.CloneStringSlice(*patch.Channels.Feishu.Recipients)
			}
		}
	}
	if patch.Scenes != nil {
		if patch.Scenes.WalletRechargeSuccess != nil {
			next.Scenes.WalletRechargeSuccess = *patch.Scenes.WalletRechargeSuccess
		}
		if patch.Scenes.OrderPaidSuccess != nil {
			next.Scenes.OrderPaidSuccess = *patch.Scenes.OrderPaidSuccess
		}
		if patch.Scenes.ManualFulfillmentPending != nil {
			next.Scenes.ManualFulfillmentPending = *patch.Scenes.ManualFulfillmentPending
		}
		if patch.Scenes.ExceptionAlert != nil {
			next.Scenes.ExceptionAlert = *patch.Scenes.ExceptionAlert
		}
	}
	if patch.Templates != nil {
		if patch.Templates.WalletRechargeSuccess != nil {
			applyNotificationSceneTemplatePatch(&next.Templates.WalletRechargeSuccess, patch.Templates.WalletRechargeSuccess)
		}
		if patch.Templates.OrderPaidSuccess != nil {
			applyNotificationSceneTemplatePatch(&next.Templates.OrderPaidSuccess, patch.Templates.OrderPaidSuccess)
		}
		if patch.Templates.ManualFulfillmentPending != nil {
			applyNotificationSceneTemplatePatch(&next.Templates.ManualFulfillmentPending, patch.Templates.ManualFulfillmentPending)
		}
		if patch.Templates.ExceptionAlert != nil {
			applyNotificationSceneTemplatePatch(&next.Templates.ExceptionAlert, patch.Templates.ExceptionAlert)
		}
	}

	normalized := NormalizeNotificationCenterSetting(next)
	if err := ValidateNotificationCenterSetting(normalized); err != nil {
		return NotificationCenterSetting{}, err
	}
	return normalized, nil
}

// IsSceneEnabled 判断通知场景是否开启
func (s NotificationSceneSetting) IsSceneEnabled(eventType string) bool {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case constants.NotificationEventWalletRechargeSuccess:
		return s.WalletRechargeSuccess
	case constants.NotificationEventOrderPaidSuccess:
		return s.OrderPaidSuccess
	case constants.NotificationEventManualFulfillmentPending:
		return s.ManualFulfillmentPending
	case constants.NotificationEventExceptionAlert, constants.NotificationEventExceptionAlertCheck:
		return s.ExceptionAlert
	default:
		return false
	}
}

// TemplateByEvent 获取事件模板
func (s NotificationTemplatesSetting) TemplateByEvent(eventType string) NotificationSceneTemplate {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case constants.NotificationEventWalletRechargeSuccess:
		return s.WalletRechargeSuccess
	case constants.NotificationEventOrderPaidSuccess:
		return s.OrderPaidSuccess
	case constants.NotificationEventManualFulfillmentPending:
		return s.ManualFulfillmentPending
	case constants.NotificationEventExceptionAlert, constants.NotificationEventExceptionAlertCheck:
		return s.ExceptionAlert
	default:
		return s.ExceptionAlert
	}
}

// ResolveLocaleTemplate 按语言选择模板
func (s NotificationSceneTemplate) ResolveLocaleTemplate(locale string) NotificationLocalizedTemplate {
	normalized := NormalizeNotificationLocale(locale)
	switch normalized {
	case constants.LocaleZhTW:
		return s.ZHTW
	case constants.LocaleEnUS:
		return s.ENUS
	default:
		return s.ZHCN
	}
}

func DecodeNotificationCenterSetting(raw jsonmap.JSON, fallback NotificationCenterSetting) NotificationCenterSetting {
	next := fallback
	if raw == nil {
		return next
	}
	legacyEnabled := settingsvalue.ReadBool(raw, "enabled", true)
	next.DefaultLocale = settingsvalue.ReadString(raw, "default_locale", next.DefaultLocale)
	next.DedupeTTLSeconds = settingsvalue.ReadInt(raw, "dedupe_ttl_seconds", next.DedupeTTLSeconds)
	next.InventoryAlertIntervalSeconds = settingsvalue.ReadInt(raw, "inventory_alert_interval_seconds", next.InventoryAlertIntervalSeconds)
	next.PaymentOrderAlertIntervalSeconds = settingsvalue.ReadInt(raw, "payment_order_alert_interval_seconds", settingsvalue.ReadInt(raw, "payment_failed_alert_interval_seconds", next.PaymentOrderAlertIntervalSeconds))
	next.PaymentOrderAlertCheckSeconds = settingsvalue.ReadInt(raw, "payment_order_alert_check_interval_seconds", next.PaymentOrderAlertCheckSeconds)
	next.IgnoredProductIDs = settingsvalue.ReadUintList(raw, "ignored_product_ids", next.IgnoredProductIDs)
	next.UpstreamBalanceAlertEnabled = settingsvalue.ReadBool(raw, "upstream_balance_alert_enabled", next.UpstreamBalanceAlertEnabled)
	next.UpstreamBalanceAlertThresholdUSD = readNotificationAmountText(raw, "upstream_balance_alert_threshold_usd", next.UpstreamBalanceAlertThresholdUSD)
	next.UpstreamCardBalanceAlertThresholdUSD = readNotificationAmountText(raw, "upstream_card_balance_alert_threshold_usd", next.UpstreamCardBalanceAlertThresholdUSD)
	next.UpstreamBalanceAlertCheckMinutes = settingsvalue.ReadInt(raw, "upstream_balance_alert_check_interval_minutes", next.UpstreamBalanceAlertCheckMinutes)
	next.UpstreamBalanceAlertRepeatHours = settingsvalue.ReadInt(raw, "upstream_balance_alert_repeat_interval_hours", next.UpstreamBalanceAlertRepeatHours)

	if channelsMap := settingsvalue.ToStringAnyMap(raw["channels"]); channelsMap != nil {
		if emailMap := settingsvalue.ToStringAnyMap(channelsMap["email"]); emailMap != nil {
			next.Channels.Email.Enabled = settingsvalue.ReadBool(emailMap, "enabled", next.Channels.Email.Enabled)
			next.Channels.Email.Recipients = settingsvalue.ReadStringList(emailMap, "recipients", next.Channels.Email.Recipients)
		}
		if telegramMap := settingsvalue.ToStringAnyMap(channelsMap["telegram"]); telegramMap != nil {
			next.Channels.Telegram.Enabled = settingsvalue.ReadBool(telegramMap, "enabled", next.Channels.Telegram.Enabled)
			next.Channels.Telegram.Recipients = settingsvalue.ReadStringList(telegramMap, "recipients", next.Channels.Telegram.Recipients)
		}
		if feishuMap := settingsvalue.ToStringAnyMap(channelsMap["feishu"]); feishuMap != nil {
			next.Channels.Feishu.Enabled = settingsvalue.ReadBool(feishuMap, "enabled", next.Channels.Feishu.Enabled)
			next.Channels.Feishu.AppID = settingsvalue.ReadString(feishuMap, "app_id", next.Channels.Feishu.AppID)
			next.Channels.Feishu.AppSecret = settingsvalue.ReadString(feishuMap, "app_secret", next.Channels.Feishu.AppSecret)
			next.Channels.Feishu.ReceiveIDType = settingsvalue.ReadString(feishuMap, "receive_id_type", next.Channels.Feishu.ReceiveIDType)
			next.Channels.Feishu.Recipients = settingsvalue.ReadStringList(feishuMap, "recipients", next.Channels.Feishu.Recipients)
		}
	}

	if scenesMap := settingsvalue.ToStringAnyMap(raw["scenes"]); scenesMap != nil {
		next.Scenes.WalletRechargeSuccess = settingsvalue.ReadBool(scenesMap, "wallet_recharge_success", next.Scenes.WalletRechargeSuccess)
		next.Scenes.OrderPaidSuccess = settingsvalue.ReadBool(scenesMap, "order_paid_success", next.Scenes.OrderPaidSuccess)
		next.Scenes.ManualFulfillmentPending = settingsvalue.ReadBool(scenesMap, "manual_fulfillment_pending", next.Scenes.ManualFulfillmentPending)
		next.Scenes.ExceptionAlert = settingsvalue.ReadBool(scenesMap, "exception_alert", next.Scenes.ExceptionAlert)
	}

	if templatesMap := settingsvalue.ToStringAnyMap(raw["templates"]); templatesMap != nil {
		if sceneMap := settingsvalue.ToStringAnyMap(templatesMap["wallet_recharge_success"]); sceneMap != nil {
			next.Templates.WalletRechargeSuccess = notificationSceneTemplateFromMap(sceneMap, next.Templates.WalletRechargeSuccess)
		}
		if sceneMap := settingsvalue.ToStringAnyMap(templatesMap["order_paid_success"]); sceneMap != nil {
			next.Templates.OrderPaidSuccess = notificationSceneTemplateFromMap(sceneMap, next.Templates.OrderPaidSuccess)
		}
		if sceneMap := settingsvalue.ToStringAnyMap(templatesMap["manual_fulfillment_pending"]); sceneMap != nil {
			next.Templates.ManualFulfillmentPending = notificationSceneTemplateFromMap(sceneMap, next.Templates.ManualFulfillmentPending)
		}
		if sceneMap := settingsvalue.ToStringAnyMap(templatesMap["exception_alert"]); sceneMap != nil {
			next.Templates.ExceptionAlert = notificationSceneTemplateFromMap(sceneMap, next.Templates.ExceptionAlert)
		}
	}
	if !legacyEnabled {
		next.Channels.Email.Enabled = false
		next.Channels.Telegram.Enabled = false
		next.Channels.Feishu.Enabled = false
	}

	return next
}

func notificationSceneTemplateFromMap(raw map[string]interface{}, fallback NotificationSceneTemplate) NotificationSceneTemplate {
	next := fallback
	if zhCNMap := settingsvalue.ToStringAnyMap(raw[constants.LocaleZhCN]); zhCNMap != nil {
		next.ZHCN = notificationLocalizedTemplateFromMap(zhCNMap, next.ZHCN)
	}
	if zhTWMap := settingsvalue.ToStringAnyMap(raw[constants.LocaleZhTW]); zhTWMap != nil {
		next.ZHTW = notificationLocalizedTemplateFromMap(zhTWMap, next.ZHTW)
	}
	if enUSMap := settingsvalue.ToStringAnyMap(raw[constants.LocaleEnUS]); enUSMap != nil {
		next.ENUS = notificationLocalizedTemplateFromMap(enUSMap, next.ENUS)
	}
	return next
}

func notificationLocalizedTemplateFromMap(raw map[string]interface{}, fallback NotificationLocalizedTemplate) NotificationLocalizedTemplate {
	next := fallback
	next.Title = settingsvalue.ReadString(raw, "title", next.Title)
	next.Body = settingsvalue.ReadString(raw, "body", next.Body)
	return next
}

func notificationSceneTemplateToMap(template NotificationSceneTemplate) map[string]interface{} {
	return map[string]interface{}{
		constants.LocaleZhCN: map[string]interface{}{
			"title": strings.TrimSpace(template.ZHCN.Title),
			"body":  strings.TrimSpace(template.ZHCN.Body),
		},
		constants.LocaleZhTW: map[string]interface{}{
			"title": strings.TrimSpace(template.ZHTW.Title),
			"body":  strings.TrimSpace(template.ZHTW.Body),
		},
		constants.LocaleEnUS: map[string]interface{}{
			"title": strings.TrimSpace(template.ENUS.Title),
			"body":  strings.TrimSpace(template.ENUS.Body),
		},
	}
}

func normalizeNotificationTemplates(templates NotificationTemplatesSetting) NotificationTemplatesSetting {
	templates.WalletRechargeSuccess = normalizeNotificationSceneTemplate(templates.WalletRechargeSuccess)
	templates.OrderPaidSuccess = normalizeNotificationSceneTemplate(templates.OrderPaidSuccess)
	templates.ManualFulfillmentPending = normalizeNotificationSceneTemplate(templates.ManualFulfillmentPending)
	templates.ExceptionAlert = normalizeNotificationSceneTemplate(templates.ExceptionAlert)
	return templates
}

func normalizeNotificationSceneTemplate(template NotificationSceneTemplate) NotificationSceneTemplate {
	template.ZHCN = normalizeNotificationLocalizedTemplate(template.ZHCN)
	template.ZHTW = normalizeNotificationLocalizedTemplate(template.ZHTW)
	template.ENUS = normalizeNotificationLocalizedTemplate(template.ENUS)
	return template
}

func normalizeNotificationLocalizedTemplate(template NotificationLocalizedTemplate) NotificationLocalizedTemplate {
	template.Title = strings.TrimSpace(template.Title)
	template.Body = strings.TrimSpace(template.Body)
	return template
}

func NormalizeNotificationLocale(locale string) string {
	normalized := strings.TrimSpace(locale)
	if _, ok := notificationSupportedLocales[normalized]; ok {
		return normalized
	}
	return constants.LocaleZhCN
}

func normalizeNotificationDedupeTTL(ttl int) int {
	if ttl < 30 || ttl > 86400 {
		return 300
	}
	return ttl
}

func NormalizeNotificationInventoryAlertInterval(ttl int) int {
	if ttl < notificationInventoryAlertIntervalMinSeconds || ttl > notificationInventoryAlertIntervalMaxSeconds {
		return notificationInventoryAlertIntervalDefaultSeconds
	}
	return ttl
}

// NormalizeNotificationPaymentOrderAlertInterval 归一化支付订单告警发送间隔。
func NormalizeNotificationPaymentOrderAlertInterval(ttl int) int {
	if ttl < notificationPaymentOrderAlertIntervalMinSeconds || ttl > notificationPaymentOrderAlertIntervalMaxSeconds {
		return notificationPaymentOrderAlertIntervalDefaultSeconds
	}
	return ttl
}

// NormalizeNotificationPaymentOrderAlertCheck 归一化支付订单告警检查区间。
func NormalizeNotificationPaymentOrderAlertCheck(seconds int) int {
	if seconds < notificationPaymentOrderAlertCheckMinSeconds || seconds > notificationPaymentOrderAlertCheckMaxSeconds {
		return notificationPaymentOrderAlertCheckDefaultSeconds
	}
	return seconds
}

// NormalizeNotificationUpstreamBalanceAlertCheckMinutes 归一化上游余额查询间隔（分钟）。
func NormalizeNotificationUpstreamBalanceAlertCheckMinutes(minutes int) int {
	if minutes < notificationUpstreamBalanceAlertCheckMinMinutes || minutes > notificationUpstreamBalanceAlertCheckMaxMinutes {
		return notificationUpstreamBalanceAlertCheckDefaultMinutes
	}
	return minutes
}

// NormalizeNotificationUpstreamBalanceAlertRepeatHours 归一化上游余额告警重复提醒间隔（小时）。
func NormalizeNotificationUpstreamBalanceAlertRepeatHours(hours int) int {
	if hours < notificationUpstreamBalanceAlertRepeatMinHours || hours > notificationUpstreamBalanceAlertRepeatMaxHours {
		return notificationUpstreamBalanceAlertRepeatDefaultHours
	}
	return hours
}

// NormalizeNotificationUpstreamBalanceThreshold 把阈值归一化为两位小数字符串；空、零或非法值归一为空（不检查）。
func NormalizeNotificationUpstreamBalanceThreshold(value string) string {
	threshold, err := parseNotificationUpstreamBalanceThreshold(value)
	if err != nil {
		return ""
	}
	return threshold
}

// UpstreamAccountBalanceThreshold 返回账户可消费余额告警阈值；未配置（空或零）时 ok 为 false。
func (s NotificationCenterSetting) UpstreamAccountBalanceThreshold() (decimal.Decimal, bool) {
	return upstreamBalanceThresholdValue(s.UpstreamBalanceAlertThresholdUSD)
}

// UpstreamCardBalanceThreshold 返回付款卡可用额度告警阈值；未配置（空或零）时 ok 为 false。
func (s NotificationCenterSetting) UpstreamCardBalanceThreshold() (decimal.Decimal, bool) {
	return upstreamBalanceThresholdValue(s.UpstreamCardBalanceAlertThresholdUSD)
}

func upstreamBalanceThresholdValue(value string) (decimal.Decimal, bool) {
	normalized := NormalizeNotificationUpstreamBalanceThreshold(value)
	if normalized == "" {
		return decimal.Zero, false
	}
	threshold, err := decimal.NewFromString(normalized)
	if err != nil || !threshold.IsPositive() {
		return decimal.Zero, false
	}
	return threshold, true
}

// parseNotificationUpstreamBalanceThreshold 校验阈值输入：空或零返回空字符串（不检查），否则返回两位小数字符串。
func parseNotificationUpstreamBalanceThreshold(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	threshold, err := decimal.NewFromString(trimmed)
	if err != nil || threshold.IsNegative() || threshold.GreaterThan(decimal.NewFromInt(notificationUpstreamBalanceAlertThresholdMaxUSDValue)) {
		return "", fmt.Errorf("%w: 上游余额告警阈值需为 0-%d 之间的美元金额", ErrNotificationConfigInvalid, notificationUpstreamBalanceAlertThresholdMaxUSDValue)
	}
	threshold = threshold.Round(2)
	if threshold.IsZero() {
		return "", nil
	}
	return threshold.StringFixed(2), nil
}

// readNotificationAmountText 读取金额字段，兼容字符串与数字两种存储形式。
func readNotificationAmountText(source map[string]interface{}, key, fallback string) string {
	value, ok := source[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return decimal.NewFromFloat(typed).String()
	case int:
		return decimal.NewFromInt(int64(typed)).String()
	case int64:
		return decimal.NewFromInt(typed).String()
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return fallback
	}
}

func normalizeEmailRecipients(items []string) []string {
	return normalizeNotificationStringList(items, true)
}

func normalizeTelegramRecipients(items []string) []string {
	result := normalizeNotificationStringList(items, false)
	filtered := make([]string, 0, len(result))
	for _, item := range result {
		if telegramChatIDPattern.MatchString(item) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func normalizeFeishuReceiveIDType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return FeishuReceiveIDTypeChatID
	}
	return normalized
}

func normalizeFeishuRecipients(items []string, receiveIDType string) []string {
	return normalizeNotificationStringList(items, receiveIDType == FeishuReceiveIDTypeEmail)
}

func normalizeNotificationStringList(items []string, lower bool) []string {
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := trimmed
		if lower {
			key = strings.ToLower(trimmed)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if lower {
			trimmed = strings.ToLower(trimmed)
		}
		result = append(result, trimmed)
	}
	return result
}

func normalizeNotificationIgnoredProductIDs(items []uint) []uint {
	if len(items) == 0 {
		return []uint{}
	}
	result := make([]uint, 0, len(items))
	seen := make(map[uint]struct{}, len(items))
	for _, item := range items {
		if item == 0 {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func applyNotificationSceneTemplatePatch(target *NotificationSceneTemplate, patch *NotificationSceneTemplatePatch) {
	if target == nil || patch == nil {
		return
	}
	applyNotificationLocalizedTemplatePatch(&target.ZHCN, patch.ZHCN)
	applyNotificationLocalizedTemplatePatch(&target.ZHTW, patch.ZHTW)
	applyNotificationLocalizedTemplatePatch(&target.ENUS, patch.ENUS)
}

func applyNotificationLocalizedTemplatePatch(target *NotificationLocalizedTemplate, patch *NotificationLocalizedTemplatePatch) {
	if target == nil || patch == nil {
		return
	}
	if patch.Title != nil {
		target.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Body != nil {
		target.Body = strings.TrimSpace(*patch.Body)
	}
}
