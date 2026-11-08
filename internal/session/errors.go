package session

import "errors"

// errNoKeyring 表示当前平台没有接钥匙串，走本地文件兜底。
var errNoKeyring = errors.New("当前平台没有接系统钥匙串")
