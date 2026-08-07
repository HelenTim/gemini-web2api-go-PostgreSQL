package app

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

// 会话保活。
//
// 浏览器每 10 分钟往 accounts.google.com/RotateCookies 打一次，响应体
// [["identity.hfcr",600],["di",N]] 里的 600 就是服务端指定的下次间隔。
//
// 它**会**返回 Set-Cookie，刷新 SIDCC / __Secure-1PSIDCC / __Secure-3PSIDCC 三项
// （两份抓包 12 次里 9 次、15 次里 14 次），跟普通响应刷的是同一组，所以这里也要
// 走 mergeSetCookie。整份抓包里没有任何响应重设过 __Secure-1PSIDTS。
//
// 注意：这条结论一开始记反了（记成"一次都没有 Set-Cookie，是纯服务端保活"），
// 因为读了 CDP 抓包的 responseHeaders —— set-cookie 只出现在 wireResponseHeaders，
// 前者恒为 0 条。改这里之前先读 docs/local/capture-reading.md。
//
// 保活能不能延长账号寿命**没有验证到**：同一来源的 cookie，带续期+保活的活了 86
// 分钟，两者都没有的活了 114 分钟，n=3 且都是从抓包里抽出来、跟真实浏览器共用同一
// 个会话。所以这里只声明"跟浏览器行为一致"，不声明"号能活更久"。

const (
	rotatePageURL = "https://accounts.google.com/RotateCookiesPage" +
		"?og_pid=658&rot=3&origin=https%3A%2F%2Fgemini.google.com&exp_id=0"
	rotatePostURL = "https://accounts.google.com/RotateCookies"
	// og_pid 是产品标识，Gemini 固定 658；它既作为上面页面的 query，也回显在页面里。
	rotateProductID = 658
	// 服务端没给出间隔时的兜底值。
	defaultRotateInterval = 10 * time.Minute
)

// 页面里形如：init('4162200486104360679', 658.0, 0.0, 0.0, 600.0)
// 第一个参数是这个会话的标识，最后一个是下次轮转的间隔秒数。
var rotateInitRe = regexp.MustCompile(`init\('([^']{4,64})'\s*,\s*([0-9.]+)\s*,[^)]*?([0-9.]+)\s*\)`)

// rotateAccount 给一个账号做一次保活，返回服务端建议的下次间隔。
func rotateAccount(a CookieAccount) (time.Duration, error) {
	proxyURL := ""
	if a.ProxyID > 0 {
		proxyURL = proxyURLByID(a.ProxyID)
	}

	// 页面这一发自己就带 Set-Cookie（抓包里刷的是 SIDCC 三项），先收下再打 POST，
	// 否则 POST 用的还是旧值。
	cookie := a.Cookie
	id, interval, pageSet, err := fetchRotateParams(cookie, proxyURL)
	if err != nil {
		return 0, err
	}
	cookie = mergeSetCookie(cookie, pageSet)

	body := fmt.Sprintf(`[%d,"%s"]`, rotateProductID, id)
	headers := rotatePostHeaders()
	headers["Cookie"] = cookie
	status, setCookie, respBody, err := rotatePost(rotatePostURL, headers, []byte(body), proxyURL)
	if err != nil {
		return 0, err
	}
	if status != 200 {
		return 0, fmt.Errorf("RotateCookies 返回 HTTP %d: %s", status, truncate(string(respBody), 120))
	}
	cookie = mergeSetCookie(cookie, setCookie)
	if cookie != a.Cookie {
		updateAccountCookie(a.ID, cookie)
	}
	// 记下这一轮到底刷新了哪些项。上游只回 SIDCC 三项，而浏览器里
	// __Secure-1PSIDTS 一族每 8 分钟左右也在换 —— 那部分我们目前拿不到，
	// 打出来是为了下次拿到活 cookie 时能直接看出有没有变化。
	if names := setCookieNames(append(append([]string{}, pageSet...), setCookie...)); len(names) > 0 {
		logf("[rotate] 账号 #%d 刷新了 %s", a.ID, strings.Join(names, ", "))
	}
	return interval, nil
}

// setCookieNames 把 Set-Cookie 头里的名字抽出来去重，只用于日志。
func setCookieNames(headers []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range headers {
		name := h
		if i := strings.Index(name, "="); i > 0 {
			name = name[:i]
		}
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// fetchRotateParams 抓 RotateCookiesPage，取会话标识、服务端指定的间隔，以及这一发
// 自己带回的 Set-Cookie。
func fetchRotateParams(cookie, proxyURL string) (string, time.Duration, []string, error) {
	status, setCookie, body, err := rotateGet(rotatePageURL, cookie, proxyURL)
	if err != nil {
		return "", 0, nil, err
	}
	if status != 200 {
		return "", 0, nil, fmt.Errorf("取轮转页返回 HTTP %d", status)
	}
	m := rotateInitRe.FindSubmatch(body)
	if m == nil {
		// 页面拿到了却没有 init(...)，最可能是 cookie 已失效跳到了登录页。
		return "", 0, nil, fmt.Errorf("轮转页里没有 init(...)（cookie 可能已失效）")
	}
	id := string(m[1])
	interval := defaultRotateInterval
	if sec, e := strconv.ParseFloat(string(m[3]), 64); e == nil && sec >= 60 && sec <= 3600 {
		interval = time.Duration(sec) * time.Second
	}
	return id, interval, setCookie, nil
}

// 下面两组 header 逐项抄自抓包（wireHeaders，不是 headers —— 后者不含 cookie）。
// 抓包里还有 sec-ch-ua-arch / -bitness / -form-factors / -full-version-list /
// -model / -platform-version / -wow64 和 x-browser-* / x-client-data /
// x-chrome-id-consistency-request，那些是 Chrome 自己贴的浏览器身份，我们贴了反而
// 会跟 TLS 指纹对不上，所以不贴。

// rotateGet 取轮转页。它在浏览器里是个 iframe 导航，所以 sec-fetch 那组跟普通
// XHR 完全不同（dest=iframe / mode=navigate / site=same-site），别套用默认值。
func rotateGet(url, cookie, proxyURL string) (int, []string, []byte, error) {
	return rotateDo("GET", url, map[string]string{
		"Cookie":                    cookie,
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		"Referer":                   "https://gemini.google.com/",
		"Sec-Fetch-Dest":            "iframe",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "same-site",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
		"Priority":                  "u=0, i",
	}, nil, proxyURL)
}

// rotatePostHeaders 是 POST /RotateCookies 那一发的完整头，调用方只补 Cookie。
func rotatePostHeaders() map[string]string {
	return map[string]string{
		"Accept":         "*/*",
		"Content-Type":   "application/json",
		"Origin":         "https://accounts.google.com",
		"Referer":        rotatePageURL,
		"Cache-Control":  "no-cache",
		"Pragma":         "no-cache",
		"Priority":       "u=1, i",
		"Sec-Fetch-Dest": "empty",
		"Sec-Fetch-Mode": "same-origin",
		"Sec-Fetch-Site": "same-origin",
	}
}

func rotatePost(url string, headers map[string]string, body []byte, proxyURL string) (
	int, []string, []byte, error) {
	return rotateDo("POST", url, headers, body, proxyURL)
}

// rotateDo 走跟正式请求同一个出口：保活从别的 IP 发，等于告诉上游这个会话在两处活动。
//
// 两条传输路径共用同一份 header。以前只有走代理那条调 applyChromeHeaders，直连那条
// 连 User-Agent 都不发 —— 同一个账号在上游看来会因为走没走代理而呈现两种客户端。
func rotateDo(method, url string, headers map[string]string, body []byte, proxyURL string) (
	int, []string, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	merged := map[string]string{
		"User-Agent":         ChromeUA,
		"Accept-Language":    "en-US,en;q=0.9",
		"Sec-CH-UA":          `"Chromium";v="146", "Google Chrome";v="146", "Not?A_Brand";v="24"`,
		"Sec-CH-UA-Mobile":   "?0",
		"Sec-CH-UA-Platform": `"Windows"`,
	}
	for k, v := range headers {
		merged[k] = v
	}
	if proxyURL != "" {
		req, err := http.NewRequest(method, url, rdr)
		if err != nil {
			return 0, nil, nil, err
		}
		for k, v := range merged {
			req.Header.Set(k, v)
		}
		resp, err := getStdlibClient(proxyURL).Do(req)
		if err != nil {
			return 0, nil, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Values("Set-Cookie"), b, err
	}
	req, err := fhttp.NewRequest(method, url, rdr)
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range merged {
		req.Header.Set(k, v)
	}
	resp, err := getTLSClient().Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Values("Set-Cookie"), b, err
}

// rotateAllAccounts 给池子里每个启用的账号做一次保活，返回下次该等多久。
//
// 失败**不计入健康度**：保活打的是 accounts.google.com，跟对话能不能用是两码事，
// 网络抖一下就把号标成坏的，会让它在挑号时沉底，反而伤可用性。
func rotateAllAccounts() time.Duration {
	next := defaultRotateInterval
	for _, a := range accountList() {
		if a.Status != "enabled" {
			continue
		}
		iv, err := rotateAccount(a)
		if err != nil {
			logf("[rotate] 账号 #%d 保活失败: %v", a.ID, err)
			continue
		}
		if iv > 0 {
			next = iv
		}
	}
	return next
}
