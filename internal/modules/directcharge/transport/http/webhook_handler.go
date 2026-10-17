package directchargehttp

import (
	"errors"
	"io"
	"net/http"

	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"

	"github.com/gin-gonic/gin"
)

// maxWebhookBody 上游回调请求体上限
const maxWebhookBody = 64 << 10

// WebhookService 上游回调用例
type WebhookService interface {
	HandleWebhook(rawBody []byte, signature string) error
}

type WebhookHandler struct {
	service WebhookService
}

func NewWebhookHandler(service WebhookService) *WebhookHandler {
	if service == nil {
		panic("directcharge webhook handler: service is nil")
	}
	return &WebhookHandler{service: service}
}

// HandleZovoCard 接收 ZovoCard 回调：用原始请求体验签，落库后立即返回，状态以后台查单为准
func (h *WebhookHandler) HandleZovoCard(c *gin.Context) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxWebhookBody))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"ok": false})
		return
	}
	err = h.service.HandleWebhook(body, c.GetHeader("X-Signature"))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"ok": true})
	case errors.Is(err, directchargecontract.ErrWebhookSignature):
		logger.Warnw("directcharge_webhook_signature_invalid", "client_ip", c.ClientIP())
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"ok": false})
	case errors.Is(err, directchargecontract.ErrDisabled):
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"ok": false})
	default:
		logger.Warnw("directcharge_webhook_failed", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"ok": false})
	}
}
