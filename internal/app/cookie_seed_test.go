package app

import (
	"os"
	"path/filepath"
	"testing"
)

// 清空 cookie 池 + kv 里的遗留值，让每个用例从干净状态开始。
func resetCookieState(t *testing.T) {
	t.Helper()
	for _, a := range accountList() {
		_ = accountDelete(a.ID)
	}
	_ = kvSet("google_cookie", "")
	cfg.CookieFile = ""
	t.Cleanup(func() {
		for _, a := range accountList() {
			_ = accountDelete(a.ID)
		}
		_ = kvSet("google_cookie", "")
		cfg.CookieFile = ""
	})
}

// kv 里遗留的单 cookie 要搬进池子，并且**从 kv 抹掉**。
// 不抹的话用户从池子里删掉它，下次重启又会冒出来。
func TestSeedLegacyKVCookie(t *testing.T) {
	resetCookieState(t)
	const raw = "SAPISID=legacy; SID=y"
	if err := kvSet("google_cookie", raw); err != nil {
		t.Fatal(err)
	}

	seedCookiesFromConfig()

	list := accountList()
	if len(list) != 1 || list[0].Cookie != raw {
		t.Fatalf("没搬进池子: %+v", list)
	}
	if v := kvGet("google_cookie"); v != "" {
		t.Errorf("kv 没抹干净: %q", v)
	}

	// 用户把它从池子里删掉之后，再启动不该复活
	_ = accountDelete(list[0].ID)
	seedCookiesFromConfig()
	if n := len(accountList()); n != 0 {
		t.Errorf("删掉后又被塞回来了，池子里还有 %d 条", n)
	}
}

// --cookie-file 每次启动重读，所以改文件重启即生效；但不能每次都插一条重复的。
func TestSeedCookieFile(t *testing.T) {
	resetCookieState(t)
	path := filepath.Join(t.TempDir(), "cookie.txt")
	if err := os.WriteFile(path, []byte("  SAPISID=fromfile; SID=z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.CookieFile = path

	seedCookiesFromConfig()
	seedCookiesFromConfig() // 模拟第二次启动

	list := accountList()
	if len(list) != 1 {
		t.Fatalf("应只有 1 条（第二次启动要去重），实际 %d 条", len(list))
	}
	if list[0].Cookie != "SAPISID=fromfile; SID=z" {
		t.Errorf("首尾空白没去掉: %q", list[0].Cookie)
	}

	// 换了文件内容 → 视作新账号
	if err := os.WriteFile(path, []byte("SAPISID=rotated; SID=z"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedCookiesFromConfig()
	if n := len(accountList()); n != 2 {
		t.Errorf("换了 cookie 后应有 2 条，实际 %d 条", n)
	}
}

// 旧的单 cookie 路径吃两种格式，JSON 那种要归一化成裸 cookie 串再入池，
// 否则池子里会存进一整段 JSON，SAPISID 提取和后续请求全错。
func TestSeedCookieJSONForm(t *testing.T) {
	resetCookieState(t)
	if err := kvSet("google_cookie", `{"cookie":"SAPISID=fromjson; SID=w","sapisid":"fromjson"}`); err != nil {
		t.Fatal(err)
	}

	seedCookiesFromConfig()

	list := accountList()
	if len(list) != 1 {
		t.Fatalf("应有 1 条，实际 %d 条", len(list))
	}
	if list[0].Cookie != "SAPISID=fromjson; SID=w" {
		t.Errorf("JSON 没归一化: %q", list[0].Cookie)
	}
	if got := extractSAPISID(list[0].Cookie); got != "fromjson" {
		t.Errorf("入池后取不出 SAPISID: %q", got)
	}
}

// 没有任何遗留配置时不该凭空造账号。
func TestSeedCookieNoop(t *testing.T) {
	resetCookieState(t)
	seedCookiesFromConfig()
	if n := len(accountList()); n != 0 {
		t.Errorf("不该新建账号，实际 %d 条", n)
	}
	if hasCookie() {
		t.Error("池子空时 hasCookie 应为 false")
	}
}
