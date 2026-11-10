package services

import "errors"

// errSessionUnavailable 会话库没起来（数据目录不可写、密钥取不到等）。
var errSessionUnavailable = errors.New("Session storage is unavailable")
