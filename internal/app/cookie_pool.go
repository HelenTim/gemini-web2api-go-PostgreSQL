package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// CookieAccount 是 cookie 池里的一行：一个 Google 登录态账号。
type CookieAccount struct {
	ID         int64  `json:"id"`
	Label      string `json:"label"`
	Cookie     string `json:"cookie"` // 完整串，API 层按需脱敏后再返回
	Status     string `json:"status"`
	Note       string `json:"note"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
	LastOkAt   int64  `json:"last_ok_at"`
	LastError  string `json:"last_error"`
	FailCount  int64  `json:"fail_count"`
}

// extractSAPISID 从一整串 cookie 里取 SAPISID 的值，取不到返回空串。
func extractSAPISID(cookie string) string {
	for _, p := range strings.Split(cookie, "; ") {
		if eq := strings.Index(p, "="); eq > 0 && p[:eq] == "SAPISID" {
			return p[eq+1:]
		}
	}
	return ""
}

// cookieNames 返回 cookie 串里出现的所有 cookie 名（顺序保留），供 UI 展示。
func cookieNames(cookie string) []string {
	var names []string
	for _, p := range strings.Split(cookie, "; ") {
		if i := strings.Index(p, "="); i > 0 {
			names = append(names, p[:i])
		}
	}
	return names
}

// accountAdd 往池里插一条。cookie 必须含 SAPISID（否则多半没复制全）。
func accountAdd(label, cookie, note string) (int64, error) {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return 0, fmt.Errorf("cookie 不能为空")
	}
	if !strings.Contains(cookie, "SAPISID") {
		return 0, fmt.Errorf("cookie 里没有 SAPISID，多半没复制全（需要 gemini.google.com 下的完整 cookie，至少含 SID / HSID / SSID / APISID / SAPISID / __Secure-1PSID）")
	}
	res, err := getDB().Exec(
		`INSERT INTO accounts(label, cookie, status, note, created_at) VALUES (?,?,?,?,?)`,
		strings.TrimSpace(label), cookie, "enabled", strings.TrimSpace(note), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// accountList 返回池里全部账号，按 id 升序。
func accountList() []CookieAccount {
	rows, err := getDB().Query(
		`SELECT id, label, cookie, status, note, created_at, last_used_at, last_ok_at, last_error, fail_count
		 FROM accounts ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []CookieAccount
	for rows.Next() {
		var a CookieAccount
		if err := rows.Scan(&a.ID, &a.Label, &a.Cookie, &a.Status, &a.Note,
			&a.CreatedAt, &a.LastUsedAt, &a.LastOkAt, &a.LastError, &a.FailCount); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}

// accountDelete 删除一条。
func accountDelete(id int64) error {
	_, err := getDB().Exec(`DELETE FROM accounts WHERE id=?`, id)
	return err
}

// accountSetStatus 改状态（enabled / disabled）。
func accountSetStatus(id int64, status string) error {
	if status != "enabled" && status != "disabled" {
		return fmt.Errorf("非法状态 %q", status)
	}
	_, err := getDB().Exec(`UPDATE accounts SET status=? WHERE id=?`, status, id)
	return err
}

// accountUpdateMeta 改 label / note（不动 cookie 本身）。
func accountUpdateMeta(id int64, label, note string) error {
	_, err := getDB().Exec(`UPDATE accounts SET label=?, note=? WHERE id=?`,
		strings.TrimSpace(label), strings.TrimSpace(note), id)
	return err
}

// accountCount 返回 (总数, enabled 数)。
func accountCount() (int, int) {
	var total, enabled int
	_ = getDB().QueryRow(`SELECT COUNT(*), SUM(CASE WHEN status='enabled' THEN 1 ELSE 0 END) FROM accounts`).
		Scan(&total, &enabled)
	return total, enabled
}

// seedCookiesFromConfig 把启动参数和历史遗留的单 cookie 并进 cookie 池。
//
// cookie 原来也有两个入口：cookie 池，和「设置」页那个池空时才用的单 cookie 输入
// （值存 kv 的 google_cookie，或来自 --cookie-file）。跟静态代理同一个毛病 ——
// 单 cookie 路径返回的账号 ID 是 0，而 markAccountResult 开头就是 id<=0 直接返回，
// 于是**健康度一个字都不写**：fail_count 恒为 0、last_ok_at 恒为空，也没有轮转。
//
//   - kv 里的 google_cookie：一次性迁入并抹掉（不抹的话用户从池子里删掉，
//     重启又会冒出来）
//   - cfg.CookieFile（--cookie-file）：每次启动重读文件并按内容去重 upsert，
//     所以改文件重启即生效
func seedCookiesFromConfig() {
	if v := strings.TrimSpace(takeLegacyCookie()); v != "" {
		if adoptCookie(v, "原单 cookie") {
			logf("[cookie] 「设置」页的单 cookie 已迁入 cookie 池，该输入框已移除")
		}
	}
	if cfg.CookieFile != "" {
		data, err := os.ReadFile(cfg.CookieFile)
		if err != nil {
			logf("[cookie] 读不了 --cookie-file %s: %v", cfg.CookieFile, err)
		} else if v := strings.TrimSpace(string(data)); v != "" {
			if adoptCookie(v, "cookie-file") {
				logf("[cookie] --cookie-file 的 cookie 已加入 cookie 池")
			}
		}
	}
}

// adoptCookie 按 cookie 内容去重地放进池子，返回是否真的新建了。
// 单 cookie 那条路以前吃两种格式，这里一并归一化成池子要的裸 cookie 串。
func adoptCookie(raw, label string) bool {
	cookie := raw
	if strings.HasPrefix(raw, "{") {
		var obj struct {
			Cookie string `json:"cookie"`
		}
		if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj.Cookie == "" {
			logf("[cookie] %s 是 JSON 但取不出 cookie 字段，跳过", label)
			return false
		}
		cookie = obj.Cookie
	}
	for _, a := range accountList() {
		if a.Cookie == cookie {
			return false
		}
	}
	if _, err := accountAdd(label, cookie, "启动时自动导入"); err != nil {
		logf("[cookie] %s 入池失败: %v", label, err)
		return false
	}
	return true
}

// takeLegacyCookie 取出 kv 里遗留的单 cookie 并抹掉。
func takeLegacyCookie() string {
	v := strings.TrimSpace(kvGet("google_cookie"))
	if v == "" {
		return ""
	}
	_ = kvSet("google_cookie", "")
	return v
}

// pickCookieAccount 从池里挑一个 enabled 账号，按 last_used_at 最久优先，
// 挑中后立刻把 last_used_at 记为现在（下次轮到别人）。池空返回 (nil,false)。
func pickCookieAccount() (*CookieAccount, bool) {
	var a CookieAccount
	err := getDB().QueryRow(
		`SELECT id, label, cookie, status, note, created_at, last_used_at, last_ok_at, last_error, fail_count
		 FROM accounts WHERE status='enabled' ORDER BY last_used_at ASC, id ASC LIMIT 1`).
		Scan(&a.ID, &a.Label, &a.Cookie, &a.Status, &a.Note,
			&a.CreatedAt, &a.LastUsedAt, &a.LastOkAt, &a.LastError, &a.FailCount)
	if err != nil {
		return nil, false
	}
	_, _ = getDB().Exec(`UPDATE accounts SET last_used_at=? WHERE id=?`, time.Now().Unix(), a.ID)
	return &a, true
}

// markCookieByStatus 按上游返回回写 cookie 健康度。
//
// 只把明确的鉴权失败（401/403）算作 cookie 的错。网络错误、代理失败、302 → sorry
// （IP 被 Google 拦）一律不计——实测住宅代理出口退化率高达 75%，把这些算进
// fail_count 会让它变成代理噪音，好 cookie 会被误伤成"失败最多"。
//
// statusCode 为 0 表示压根没拿到响应（网络层失败）。
func markCookieByStatus(id int64, statusCode int, errStr string) {
	switch {
	case statusCode == 200:
		markAccountResult(id, true, "")
	case statusCode == 401 || statusCode == 403:
		markAccountResult(id, false, errStr)
	default:
		// 其余情况责任不在 cookie，不动它的健康度
	}
}

// markAccountResult 请求结束后回写结果：成功清零 fail_count 并记 last_ok_at；
// 失败累加 fail_count 并记 last_error。
//
// 注意 last_ok_at 的语义是"这个 cookie 参与的请求成功过"，**不等于"cookie 仍然
// 有效"**：cookie 过期后 Gemini 不报错，只是把你当匿名用户，纯文本请求照样 200。
// 要真正验有效性，最省事的判据是请求 gemini-3.1-pro：cookie 有效时服务端回报
// "3.1 Pro" 并带思考链，失效时静默降级成 3.5 Flash-Lite。（xsrf.go 取 token 时
// 若页面里没有 SNlM0e，也会当场判定 cookie 失效。）
func markAccountResult(id int64, ok bool, errStr string) {
	if id <= 0 {
		return
	}
	if ok {
		_, _ = getDB().Exec(
			`UPDATE accounts SET last_ok_at=?, fail_count=0, last_error='' WHERE id=?`,
			time.Now().Unix(), id)
		return
	}
	_, _ = getDB().Exec(
		`UPDATE accounts SET fail_count=fail_count+1, last_error=? WHERE id=?`,
		truncate(errStr, 200), id)
}
