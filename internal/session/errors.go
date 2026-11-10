package session

import "errors"

// errNoKeyring 表示当前平台没有接钥匙串，走本地文件兜底。
var errNoKeyring = errors.New("The system keychain is not wired up on this platform")
