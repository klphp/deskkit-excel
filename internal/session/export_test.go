package session

import "database/sql"

// openRaw 仅供测试：用任意 DSN 打开库，模拟「错误密钥」。
func openRaw(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
