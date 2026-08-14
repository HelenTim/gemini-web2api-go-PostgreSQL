package app

import (
	"fmt"
	"os"
	"testing"
)

// TestMain 把 DB 指向一个 PostgreSQL 实例再跑测试。
//
// hasCookie() 现在只看 cookie 池，也就是要查 DB；不接管 cfg 的话 getDB() 会去连
// DATABASE_URL。测试需要一台可写的 PostgreSQL（CI 里由 postgres service 提供），
// 通过 TEST_DATABASE_URL 指定，否则直接跳过（本地没起库时不让 `go test` 挂掉）。
func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "skip: 未设置 TEST_DATABASE_URL / DATABASE_URL，跳过需要数据库的测试")
		os.Exit(0)
	}
	cfg.DatabaseURL = dsn
	os.Exit(m.Run())
}

// withPoolCookie 往 cookie 池塞一条测试账号，用完自动删掉。
// 取代原来的 cookieRuntime.Store —— 那条单 cookie 路径已经取消。
func withPoolCookie(t *testing.T) {
	t.Helper()
	id, err := accountAdd("test", "SAPISID=dummy; SID=x", "")
	if err != nil {
		t.Fatalf("插测试 cookie 失败: %v", err)
	}
	t.Cleanup(func() { _ = accountDelete(id) })
}
