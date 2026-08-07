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

	id, interval, err := fetchRotateParams(a.Cookie, proxyURL)
	if err != nil {
		return 0, err
	}
	body := fmt.Sprintf(`[%d,"%s"]`, rotateProductID, id)
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Origin":        "https://accounts.google.com",
		"Referer":       rotatePageURL,
		"Cookie":        a.Cookie,
		"Accept":        "*/*",
		"Cache-Control": "no-cache",
	}
	status, setCookie, respBody, err := rotatePost(rotatePostURL, headers, []byte(body), proxyURL)
	if err != nil {
		return 0, err
	}
	if status != 200 {
		return 0, fmt.Errorf("RotateCookies 返回 HTTP %d: %s", status, truncate(string(respBody), 120))
	}
	// 抓包里这个响应不带 Set-Cookie，但合并一下不亏 —— 真带了就是免费的续命。
	if merged := mergeSetCookie(a.Cookie, setCookie); merged != a.Cookie {
		updateAccountCookie(a.ID, merged)
	}
	return interval, nil
}

// fetchRotateParams 抓 RotateCookiesPage，取会话标识和服务端指定的间隔。
func fetchRotateParams(cookie, proxyURL string) (string, time.Duration, error) {
	status, _, body, err := rotateGet(rotatePageURL, cookie, proxyURL)
	if err != nil {
		return "", 0, err
	}
	if status != 200 {
		return "", 0, fmt.Errorf("取轮转页返回 HTTP %d", status)
	}
	m := rotateInitRe.FindSubmatch(body)
	if m == nil {
		// 页面拿到了却没有 init(...)，最可能是 cookie 已失效跳到了登录页。
		return "", 0, fmt.Errorf("轮转页里没有 init(...)（cookie 可能已失效）")
	}
	id := string(m[1])
	interval := defaultRotateInterval
	if sec, e := strconv.ParseFloat(string(m[3]), 64); e == nil && sec >= 60 && sec <= 3600 {
		interval = time.Duration(sec) * time.Second
	}
	return id, interval, nil
}

func rotateGet(url, cookie, proxyURL string) (int, []string, []byte, error) {
	return rotateDo("GET", url, map[string]string{
		"Cookie": cookie,
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}, nil, proxyURL)
}

func rotatePost(url string, headers map[string]string, body []byte, proxyURL string) (
	int, []string, []byte, error) {
	return rotateDo("POST", url, headers, body, proxyURL)
}

// rotateDo 走跟正式请求同一个出口：保活从别的 IP 发，等于告诉上游这个会话在两处活动。
func rotateDo(method, url string, headers map[string]string, body []byte, proxyURL string) (
	int, []string, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	if proxyURL != "" {
		req, err := http.NewRequest(method, url, rdr)
		if err != nil {
			return 0, nil, nil, err
		}
		applyChromeHeaders(req)
		for k, v := range headers {
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
	for k, v := range headers {
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
