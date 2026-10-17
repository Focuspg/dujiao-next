package contract

import "errors"

var (
	ErrGatewayConfigInvalid      = errors.New("payment provider config invalid")
	ErrGatewayRequestFailed      = errors.New("payment provider request failed")
	ErrGatewayResponseInvalid    = errors.New("payment provider response invalid")
	ErrGatewaySignatureInvalid   = errors.New("payment provider signature invalid")
	ErrGatewayAuthFailed         = errors.New("payment provider auth failed")
	ErrGatewayUnsupportedChannel = errors.New("payment channel type not supported by provider")
	ErrGatewayProviderNotFound   = errors.New("payment provider not found in registry")
	// ErrGatewayRejected 网关明确拒绝了请求（如退款单不存在）。与其他错误同时成立时，说明请求已到达网关。
	ErrGatewayRejected = errors.New("payment provider rejected request")
)
