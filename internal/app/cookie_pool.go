package app

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
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
	ProxyID    int64  `json:"proxy_id"` // 绑定的出口，0 = 还没绑
}

// splitCookiePairs 把 "k=v; k=v" 拆成键值对。
//
// 按 ";" 切再逐段 TrimSpace，不按 "; " 切：从 DevTools 复制出来的串不一定带空格，
// 而按 "; " 切的话 "SID=a;SAPISID=b" 会整段当成一个键 —— 于是 extractSAPISID
// 取不到值、不发 Authorization 头、请求被上游当匿名处理，用户毫不知情。
//
// 值里可能含 "="（base64 补位），所以只在第一个等号处切。
func splitCookiePairs(cookie string) [][2]string {
	var out [][2]string
	for _, p := range strings.Split(cookie, ";") {
		p = strings.TrimSpace(p)
		if i := strings.Index(p, "="); i > 0 {
			out = append(out, [2]string{p[:i], p[i+1:]})
		}
	}
	return out
}

// extractSAPISID 从一整串 cookie 里取 SAPISID 的值，取不到返回空串。
func extractSAPISID(cookie string) string {
	for _, kv := range splitCookiePairs(cookie) {
		if kv[0] == "SAPISID" {
			return kv[1]
		}
	}
	return ""
}

// cookieNames 返回 cookie 串里出现的所有 cookie 名（顺序保留），供 UI 展示。
func cookieNames(cookie string) []string {
	var names []string
	for _, kv := range splitCookiePairs(cookie) {
		names = append(names, kv[0])
	}
	return names
}

// accountAdd 往池里插一条。cookie 必须能取到非空的 SAPISID。
// 这是面板/API 手工添加走的路，用户当场看得到错误提示，拦下来是对的。
//
// 判据是**取不取得到值**，不是"字符串里出没出现过 SAPISID"：后者会把
// 一份没填的逐项模板（键名在、值全空）放进池子，存成一整段 JSON，
// 之后每次轮到它都注定失败。
func accountAdd(label, cookie, note string) (int64, error) {
	c, ok := normalizeCookie(cookie, "手工添加的 cookie")
	if !ok {
		return 0, fmt.Errorf("cookie 解析失败：既不是 \"k=v; k=v\" 串，也不是可识别的 JSON")
	}
	if extractSAPISID(c) == "" {
		return 0, fmt.Errorf("cookie 里没有 SAPISID，多半没复制全（需要 gemini.google.com 下的完整 cookie，至少含 SID / HSID / SSID / APISID / SAPISID / __Secure-1PSID）")
	}
	return accountInsert(label, c, note)
}

// accountAdopt 是启动时导入既有配置专用的：**不做 SAPISID 检查**。
//
// 判据是「升级不能改变用户已有配置的可用性」。缺 SAPISID 的 cookie 在旧版是被
// 原样当 Cookie 头发出去的（只是算不出 SAPISIDHASH 授权头），能不能用由上游说了算 ——
// 不该在升级时被我们新加的校验拦下来，让用户悄无声息地退回匿名。只警告，
// 健康度会在面板上如实体现。
func accountAdopt(label, cookie, note string) (int64, error) {
	cookie, ok := normalizeCookie(cookie, label)
	if !ok {
		return 0, fmt.Errorf("cookie 解析失败")
	}
	if extractSAPISID(cookie) == "" {
		logf("[cookie] %s 里没有 SAPISID，算不出 SAPISIDHASH 授权头，可能只当匿名处理；"+
			"先按原样导入，请到面板核对", label)
	}
	return accountInsert(label, cookie, note)
}

func accountInsert(label, cookie, note string) (int64, error) {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return 0, fmt.Errorf("cookie 不能为空")
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
		`SELECT id, label, cookie, status, note, created_at, last_used_at, last_ok_at, last_error, fail_count, proxy_id
		 FROM accounts ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []CookieAccount
	for rows.Next() {
		var a CookieAccount
		if err := rows.Scan(&a.ID, &a.Label, &a.Cookie, &a.Status, &a.Note,
			&a.CreatedAt, &a.LastUsedAt, &a.LastOkAt, &a.LastError, &a.FailCount, &a.ProxyID); err != nil {
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

// kv 里记迁移/播种状态的键。
const (
	kvLegacyCookieDone = "legacy_single_cookie_migrated"
	kvSeededCookieID   = "seeded_cookie_id"
	kvSeededCookieVal  = "seeded_cookie_value"
)

// seedCookiesFromConfig 把启动参数和历史遗留的单 cookie 并进 cookie 池。
//
// cookie 原来也有两个入口：cookie 池，和「设置」页那个池空时才用的单 cookie 输入
// （值存 kv 的 google_cookie，或来自 --cookie-file）。跟静态代理同一个毛病 ——
// 单 cookie 路径返回的账号 ID 是 0，而 markAccountResult 开头就是 id<=0 直接返回，
// 于是**健康度一个字都不写**：fail_count 恒为 0、last_ok_at 恒为空，也没有轮转。
//
//   - kv 里的 google_cookie：一次性迁入，用独立标记记"迁过了"，原值不动
//   - cfg.CookieFile（--cookie-file）：声明式跟随，内容变了替换同一条记录
func seedCookiesFromConfig() {
	migrateLegacyCookie()
	syncSeededCookieFile()
}

// migrateLegacyCookie 一次性把 kv 里遗留的单 cookie 搬进池子。
// 三条保命规则：走 accountAdopt 不做 SAPISID 校验（旧路径不校验，升级不该改变
// 可用性）、入池成功才标记完成、不去动 kv 里原来的值（回滚到旧版仍可用）。
func migrateLegacyCookie() {
	if kvGet(kvLegacyCookieDone) == "1" {
		return
	}
	raw := strings.TrimSpace(kvGet("google_cookie"))
	if raw == "" {
		_ = kvSet(kvLegacyCookieDone, "1")
		return
	}
	cookie, ok := normalizeCookie(raw, "「设置」页的单 cookie")
	if !ok {
		return
	}
	if !poolHasCookie(cookie) {
		if _, err := accountAdopt("原单 cookie", cookie, "从「设置」页迁入"); err != nil {
			logf("[cookie] 「设置」页的单 cookie 迁入池子失败，原值保留、下次启动重试: %v", err)
			return
		}
		logf("[cookie] 「设置」页的单 cookie 已迁入 cookie 池")
	}
	_ = kvSet(kvLegacyCookieDone, "1")
}

// syncSeededCookieFile 让池子里跟着 --cookie-file 走一条记录。
//
// 跟 --proxy 同一套：文件内容变了就撤下旧的那条再建新的，而不是又加一条。
// 按内容去重挡不住这个 —— 定期轮换 cookie.txt 的部署会一次次往池子里堆死 cookie，
// 而它们仍然 enabled，仍然参与轮转，于是每 N 个请求就有一个注定失败。
// 内容没变时完全不碰池子，面板上的增删改停用都以面板为准。
func syncSeededCookieFile() {
	var cur string
	if cfg.CookieFile != "" {
		data, err := os.ReadFile(cfg.CookieFile)
		if err != nil {
			// 读不到就当没配过：宁可保持现状，也不能因为容器少挂一个卷
			// 就把用户在用的 cookie 撤下来。
			logf("[cookie] 读不了 --cookie-file %s，池子保持不变: %v", cfg.CookieFile, err)
			return
		}
		cur = strings.TrimSpace(string(data))
	}
	if cur != "" {
		if c, ok := normalizeCookie(cur, "--cookie-file"); ok {
			cur = c
		} else {
			return
		}
	}

	prev := kvGet(kvSeededCookieVal)
	if cur == prev {
		return
	}
	dropSeededCookie(prev)
	if cur == "" {
		_ = kvSet(kvSeededCookieVal, "")
		_ = kvSet(kvSeededCookieID, "")
		logf("[cookie] --cookie-file 已移除，对应的池子记录一并撤下")
		return
	}
	if poolHasCookie(cur) {
		_ = kvSet(kvSeededCookieVal, cur)
		_ = kvSet(kvSeededCookieID, "")
		return
	}
	id, err := accountAdopt("cookie-file", cur, "来自 --cookie-file")
	if err != nil {
		logf("[cookie] --cookie-file 入池失败: %v", err)
		return
	}
	_ = kvSet(kvSeededCookieVal, cur)
	_ = kvSet(kvSeededCookieID, strconv.FormatInt(id, 10))
	logf("[cookie] --cookie-file 的 cookie 已加入 cookie 池")
}

// dropSeededCookie 撤掉上一次由 --cookie-file 建的那条。
// 只在内容还是我们写进去的那份时才删——用户在面板改过就说明他接管了。
func dropSeededCookie(prevCookie string) {
	idStr := kvGet(kvSeededCookieID)
	if idStr == "" || prevCookie == "" {
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return
	}
	for _, a := range accountList() {
		if a.ID == id {
			if a.Cookie == prevCookie {
				if err := accountDelete(id); err != nil {
					logf("[cookie] 撤下旧的 --cookie-file 记录失败: %v", err)
				}
			}
			return
		}
	}
}

// 面板逐项填写模式列出的 cookie，同时也是拼串时的固定顺序。
// 顺序必须确定：同一份 cookie 若因键顺序不同拼出两种串，按内容去重就失效了。
var cookieTemplateOrder = []string{
	"SID", "HSID", "SSID", "APISID", "SAPISID", "__Secure-1PSID", "__Secure-1PSIDTS",
}

// normalizeCookie 把各种输入形态归一化成池子要的裸 "k=v; k=v" 串。
//
// 吃三种：
//   - 裸串，原样返回
//   - 旧单 cookie 路径的 {"cookie":"k=v; k=v","sapisid":"..."}
//   - 面板逐项模式的 {"SID":"a","SAPISID":"b",...}
//
// 不归一化的话池子里会存进一整段 JSON，SAPISID 提取和后续请求全错。
func normalizeCookie(raw, who string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") {
		return raw, true
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		logf("[cookie] %s 是 JSON 但解析失败，跳过: %v", who, err)
		return "", false
	}
	if c, ok := m["cookie"].(string); ok && strings.TrimSpace(c) != "" {
		return strings.TrimSpace(c), true
	}

	str := func(k string) string {
		s, _ := m[k].(string)
		return strings.TrimSpace(s)
	}
	var parts []string
	inTemplate := map[string]bool{}
	for _, k := range cookieTemplateOrder {
		inTemplate[k] = true
		if v := str(k); v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	// 用户从 DevTools 多复制几项进来不能丢，附在模板字段后面按名字排序
	var extra []string
	for k := range m {
		if !inTemplate[k] && k != "sapisid" && str(k) != "" {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		parts = append(parts, k+"="+str(k))
	}
	if len(parts) == 0 {
		logf("[cookie] %s 是 JSON 但一项非空值都没有，跳过", who)
		return "", false
	}
	return strings.Join(parts, "; "), true
}

func poolHasCookie(cookie string) bool {
	for _, a := range accountList() {
		if a.Cookie == cookie {
			return true
		}
	}
	return false
}

// pickCookieAccount 从池里挑一个 enabled 账号，按 last_used_at 最久优先，
// 挑中后立刻把 last_used_at 记为现在（下次轮到别人）。池空返回 (nil,false)。
func pickCookieAccount() (*CookieAccount, bool) {
	var a CookieAccount
	err := getDB().QueryRow(
		`SELECT id, label, cookie, status, note, created_at, last_used_at, last_ok_at, last_error, fail_count, proxy_id
		 FROM accounts WHERE status='enabled' ORDER BY last_used_at ASC, id ASC LIMIT 1`).
		Scan(&a.ID, &a.Label, &a.Cookie, &a.Status, &a.Note,
			&a.CreatedAt, &a.LastUsedAt, &a.LastOkAt, &a.LastError, &a.FailCount, &a.ProxyID)
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

// CookieCheck 是一次 cookie 有效性检测的结果。
type CookieCheck struct {
	OK        bool   `json:"ok"`
	Detail    string `json:"detail"`
	ProxyName string `json:"proxy_name"`
	TookMs    int64  `json:"took_ms"`
}

// checkAccountCookie 判断一条 cookie 还有没有登录态。
//
// 判据是 /app 页面里有没有 SNlM0e：cookie 失效时 Gemini 不报错，只是把你当匿名
// 用户，纯文本请求照样 200 —— 所以不能拿"请求成功"当有效性判据。这个页面没有
// SNlM0e 就说明服务端没认这个登录态。
//
// 只抓页面，不发对话，不消耗生成配额。
func checkAccountCookie(a CookieAccount) CookieCheck {
	t0 := time.Now()
	picked, ok, err := acquireSlot(a.ProxyID)
	if !ok {
		return CookieCheck{Detail: "拿不到出口：" + err.Error()}
	}
	defer releaseSlot(picked.ID)
	proxyURL := picked.URL
	name := picked.Name
	if name == "" {
		name = "直连"
	}

	// 先作废缓存，否则可能拿到几分钟前的旧结论，检测就没意义了
	invalidateXSRF(a.Cookie)
	_, err = getXSRF(a.Cookie, proxyURL)
	took := time.Since(t0).Milliseconds()
	if err != nil {
		markAccountResult(a.ID, false, err.Error())
		return CookieCheck{Detail: explainCookieFailure(err), ProxyName: name, TookMs: took}
	}
	markAccountResult(a.ID, true, "")
	detail := "登录态有效"
	if extractSAPISID(a.Cookie) == "" {
		detail += "，但缺 SAPISID（算不出授权头，部分接口会被当匿名）"
	}
	return CookieCheck{OK: true, Detail: detail, ProxyName: name, TookMs: took}
}

// explainCookieFailure 把底层错误翻成运维看得懂的结论。
func explainCookieFailure(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no SNlM0e"):
		return "cookie 已失效：页面能打开但没有登录态，请求会被当匿名处理"
	case strings.Contains(msg, "HTTP 302"):
		return "cookie 无效：被重定向到登录页"
	case strings.Contains(msg, "HTTP 429"), strings.Contains(msg, "sorry"):
		return "出口被上游限流，换个代理再试（不是 cookie 的问题）"
	default:
		return "检测失败：" + msg
	}
}

// bindAccountProxy 记住这个账号这次用的出口，下次优先复用。
func bindAccountProxy(accountID, proxyID int64) {
	if accountID <= 0 {
		return
	}
	_, _ = getDB().Exec(`UPDATE accounts SET proxy_id=? WHERE id=?`, proxyID, accountID)
}
