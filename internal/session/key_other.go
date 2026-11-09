//go:build !darwin && !windows

package session

// 非 macOS 平台先退回「本地文件 + 0600」。
//
// 为什么不在 Windows 上用 DPAPI：那需要 unsafe 调 CryptProtectData，
// 而这台开发机是 Mac，写进去也测不了 —— 没跑过的加密代码比没有加密更危险。
// 等有了 Windows 构建环境（我们会用它出包）再补，届时用 golang.org/x/sys/windows
// 的 CryptProtectData 包一层即可，接口不用改。
//
// 文件方案的边界：数据库文件被拷走依然打不开（密钥在另一个文件里），
// 但同一登录用户下的程序能同时读到两个文件。

func readKey() (string, error) { return "", errNoKeyring }

func writeKey(string) error { return errNoKeyring }
