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
	user.POST("/direct-charge/orders/:order_no/credential", handler.ResupplyCredential)
	user.POST("/direct-charge/orders/:order_no/refund-request", handler.RequestRefund)
}

// RegisterAdminRoutes 后台充值规格与任务管理
func RegisterAdminRoutes(admin gin.IRoutes, handler *AdminHandler) {
	if admin == nil || handler == nil {
		panic("directcharge admin routes: required dependency is nil")
	}
	admin.GET("/direct-charge/specs", handler.ListSpecs)
	admin.POST("/direct-charge/specs", handler.CreateSpec)
	admin.PUT("/direct-charge/specs/:id", handler.UpdateSpec)
	admin.GET("/direct-charge/tasks", handler.ListTasks)
	admin.GET("/direct-charge/tasks/:id", handler.GetTask)
	admin.POST("/direct-charge/tasks/:id/reconcile", handler.ReconcileTask)
	admin.POST("/direct-charge/tasks/:id/retry", handler.RetryTask)
	admin.POST("/direct-charge/tasks/:id/resolve", handler.ResolveTask)
	admin.POST("/direct-charge/tasks/:id/refund", handler.RefundTask)
}

// RegisterWebhookRoutes 上游回调；不需要登录，签名在用例内校验，调用方负责挂载限流
func RegisterWebhookRoutes(callbacks gin.IRoutes, handler *WebhookHandler) {
	if callbacks == nil || handler == nil {
		panic("directcharge webhook routes: required dependency is nil")
	}
	callbacks.POST("/webhooks/zovocard", handler.HandleZovoCard)
}
