package app

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

// Gemini 在带登录 cookie 时要求 batchexecute 请求多带一个表单字段 at（XSRF token），
// 不带就直接 400，响应体形如 [["er",...,400,...,[{"48448350":["xsrf", ...]}]]]。
// 匿名请求不需要它，所以这个坑一直没暴露——一挂上有效 cookie，所有请求立刻全挂。
//
// token 来自 /app 页面 HTML 里的 "SNlM0e":"<token>:<毫秒时间戳>"，跟 cookie 会话
// 绑定，所以按 cookie 分别缓存。实测同一 token 可以复用，过期后服务端还是回 xsrf
// 错误，调用方拿到这个错要 invalidate 再取一次。
var (
	xsrfMu    sync.Mutex
	xsrfCache = map[string]xsrfEntry{}
)

type xsrfEntry struct {
	token   string
	fetched time.Time
}

// 页面上的 token 没写明有效期，取个保守值定期重取。
const xsrfTTL = 20 * time.Minute

var snlm0eRe = regexp.MustCompile(`"SNlM0e":"([^"]{10,200})"`)

// cookieKey 用 cookie 的短摘要当缓存键，避免把整串凭证塞进 map key。
func cookieKey(cookie string) string {
	sum := sha1.Sum([]byte(cookie))
	return hex.EncodeToString(sum[:8])
}

// invalidateXSRF 丢掉某个 cookie 的缓存 token，下次取会重新抓页面。
func invalidateXSRF(cookie string) {
	if cookie == "" {
		return
	}
	xsrfMu.Lock()
	delete(xsrfCache, cookieKey(cookie))
	xsrfMu.Unlock()
}

// getXSRF 取该 cookie 对应的 XSRF token；命中缓存且没过期就直接返回。
// cookie 为空（匿名）时返回空串——匿名请求不需要这个字段。
func getXSRF(cookie, proxyURL string) (string, error) {
	if cookie == "" {
		return "", nil
	}
	key := cookieKey(cookie)

	xsrfMu.Lock()
	if e, ok := xsrfCache[key]; ok && time.Since(e.fetched) < xsrfTTL {
		xsrfMu.Unlock()
		return e.token, nil
	}
	xsrfMu.Unlock()

	token, err := fetchXSRF(cookie, proxyURL)
	if err != nil {
		return "", err
	}
	xsrfMu.Lock()
	xsrfCache[key] = xsrfEntry{token: token, fetched: time.Now()}
	xsrfMu.Unlock()
	return token, nil
}

// fetchXSRF 抓 /app 页面从 HTML 里抠 SNlM0e。
// 走跟主请求相同的出口：配了代理走 stdlib，没配走 tls-client，
// 免得 token 和后续请求来自两个不同 IP。
func fetchXSRF(cookie, proxyURL string) (string, error) {
	const pageURL = "https://gemini.google.com/app"
	headers := map[string]string{
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Accept-Language": "en-US,en;q=0.9",
		"Cookie":          cookie,
	}

	var body []byte
	if proxyURL != "" {
		req, err := http.NewRequest("GET", pageURL, nil)
		if err != nil {
			return "", err
		}
		applyChromeHeaders(req)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := getStdlibClient(proxyURL).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("fetch XSRF page: HTTP %d", resp.StatusCode)
		}
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
	} else {
		req, err := fhttp.NewRequest("GET", pageURL, nil)
		if err != nil {
			return "", err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := getTLSClient().Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("fetch XSRF page: HTTP %d", resp.StatusCode)
		}
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
	}

	m := snlm0eRe.FindSubmatch(body)
	if m == nil {
		// 页面拿到了却没有 token，最可能是 cookie 已失效被当成匿名用户。
		return "", fmt.Errorf("no SNlM0e in page (cookie expired or not signed in)")
	}
	return string(m[1]), nil
}

// isXSRFError 判断上游 400 是不是 XSRF token 的问题。
// 响应体形如：[["er",null,...,400,...,[{"48448350":["xsrf","<新token>",...]}]]]
func isXSRFError(raw string) bool {
	return strings.Contains(raw, `"xsrf"`)
}
