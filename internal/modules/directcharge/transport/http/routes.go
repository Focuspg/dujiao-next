package directchargehttp

import "github.com/gin-gonic/gin"

// RegisterUserReadRoutes 登录用户查询代充进度
func RegisterUserReadRoutes(user gin.IRoutes, handler *UserHandler) {
	if user == nil || handler == nil {
		panic("directcharge user routes: required dependency is nil")
	}
	user.GET("/direct-charge/orders/:order_no", handler.GetOrder)
}

// RegisterUserWriteRoutes 登录用户预检与下单；调用方负责挂载限流
func RegisterUserWriteRoutes(user gin.IRoutes, handler *UserHandler) {
	if user == nil || handler == nil {
		panic("directcharge user routes: required dependency is nil")
	}
	user.POST("/direct-charge/preflights", handler.Preflight)
	user.POST("/direct-charge/orders", handler.CreateOrder)
}

// RegisterWebhookRoutes 上游回调；不需要登录，签名在用例内校验，调用方负责挂载限流
func RegisterWebhookRoutes(callbacks gin.IRoutes, handler *WebhookHandler) {
	if callbacks == nil || handler == nil {
		panic("directcharge webhook routes: required dependency is nil")
	}
	callbacks.POST("/webhooks/zovocard", handler.HandleZovoCard)
}
