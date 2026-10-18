// Package contactlink 统一主站与分销站客服联系方式链接的校验规则。
package contactlink

import (
	"net/mail"
	"net/url"
	"strings"
)

// IsTelegram 判断是否为 Telegram 联系链接。
func IsTelegram(value string) bool {
	return strings.HasPrefix(value, "https://telegram.me/") ||
		strings.HasPrefix(value, "https://t.me/") ||
		strings.HasPrefix(value, "tg://")
}

// IsWhatsApp 判断是否为 WhatsApp 联系链接。
func IsWhatsApp(value string) bool {
	return strings.HasPrefix(value, "https://wa.me/") ||
		strings.HasPrefix(value, "https://api.whatsapp.com/")
}

// IsEmail 判断是否为合法的客服邮箱地址。
func IsEmail(value string) bool {
	_, err := mail.ParseAddress(value)
	return err == nil
}

// IsSupportURL 判断是否为可用的客服链接：https 完整链接、tg:// 或 mailto: 邮箱。
func IsSupportURL(value string) bool {
	if strings.HasPrefix(value, "tg://") {
		return true
	}
	if strings.HasPrefix(value, "mailto:") {
		return IsEmail(strings.TrimPrefix(value, "mailto:"))
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && parsed.Scheme == "https"
}
