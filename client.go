package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

var (
	clientCache   = map[string]tls_client.HttpClient{}
	clientCacheMu sync.RWMutex
)

// resolveProfile maps a config string to a tls-client ClientProfile.
// Defaults to Chrome_146 (matches Python v2's curl_cffi chrome146).
func resolveProfile(name string) profiles.ClientProfile {
	switch strings.ToLower(name) {
	case "chrome_120":
		return profiles.Chrome_120
	case "chrome_124":
		return profiles.Chrome_124
	case "chrome_131":
		return profiles.Chrome_131
	case "chrome_133":
		return profiles.Chrome_133
	case "chrome_144":
		return profiles.Chrome_144
	case "chrome_146", "chrome146":
		return profiles.Chrome_146
	case "firefox_120":
		return profiles.Firefox_120
	case "firefox_123":
		return profiles.Firefox_123
	case "firefox_147", "firefox147":
		return profiles.Firefox_147
	case "safari_16_0":
		return profiles.Safari_16_0
	case "safari_ios_17_0":
		return profiles.Safari_IOS_17_0
	default:
		return profiles.Chrome_146
	}
}

// getTLSClient returns a tls-client (Chrome 146 fingerprint) for direct connections only.
// For proxied connections we use stdlib (see getStdlibClient) — tls-client's SOCKS5
// implementation is fragile compared to net/http.
func getTLSClient() tls_client.HttpClient {
	clientCacheMu.RLock()
	if c, ok := clientCache[cfg.Impersonate]; ok {
		clientCacheMu.RUnlock()
		return c
	}
	clientCacheMu.RUnlock()

	clientCacheMu.Lock()
	defer clientCacheMu.Unlock()
	if c, ok := clientCache[cfg.Impersonate]; ok {
		return c
	}
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(cfg.RequestTimeout),
		tls_client.WithClientProfile(resolveProfile(cfg.Impersonate)),
		tls_client.WithNotFollowRedirects(),
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[client] tls-client init failed: %v\n", err)
		os.Exit(1)
	}
	clientCache[cfg.Impersonate] = client
	return client
}

// ─── Stdlib HTTP client for proxied requests ─────────────────────────────────
//
// 走代理时改用 stdlib 而不是 tls-client，因为：
// 1. stdlib 的 http.ProxyURL 原生支持 socks5:// / socks5h:// / http:// / https://，
//    跟 Kiro-Gogogo 项目同款实现，已知能过我们手头的代理。
// 2. tls-client 自带的 SOCKS 实现在某些代理端会 EOF（已实测）。
// 3. 走代理时 Google 看到的"客户端指纹"实际是代理出口节点的 TLS 握手,
//    我们这边伪不伪装意义不大;但应用层 header 还是按 Chrome 146 模拟。
//
// 不走代理时仍然用 getTLSClient（保留 utls/chrome146 真指纹优势）。

var (
	stdlibClientCache sync.Map // proxyURL -> *http.Client
)

// getStdlibClient returns an http.Client routed through proxyURL.
// proxyURL must not be empty (caller checks).
func getStdlibClient(proxyURL string) *http.Client {
	if cached, ok := stdlibClientCache.Load(proxyURL); ok {
		return cached.(*http.Client)
	}
	t := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		// 走代理时不强求 HTTP/2，部分代理不支持。
		ForceAttemptHTTP2: false,
	}
	if u, err := url.Parse(proxyURL); err == nil {
		t.Proxy = http.ProxyURL(u)
	}
	c := &http.Client{
		Timeout:   time.Duration(cfg.RequestTimeout) * time.Second,
		Transport: t,
		// 跟 tls-client 一致：不自动跟随重定向（302 是诊断信号）
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	stdlibClientCache.Store(proxyURL, c)
	return c
}

// loadCookie reads the cookie file (Netscape one-line format or JSON).
func loadCookie() (string, string) {
	if cfg.CookieFile == "" {
		return "", ""
	}
	data, err := os.ReadFile(cfg.CookieFile)
	if err != nil {
		return "", ""
	}
	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "{") {
		var obj struct {
			Cookie  string `json:"cookie"`
			Sapisid string `json:"sapisid"`
		}
		if err := json.Unmarshal([]byte(content), &obj); err != nil {
			return "", ""
		}
		return obj.Cookie, obj.Sapisid
	}
	cookieStr := content
	sapisid := ""
	for _, p := range strings.Split(cookieStr, "; ") {
		if eq := strings.Index(p, "="); eq > 0 {
			if p[:eq] == "SAPISID" {
				sapisid = p[eq+1:]
			}
		}
	}
	return cookieStr, sapisid
}

func makeSAPISIDHash(sapisid string) string {
	ts := time.Now().Unix()
	h := sha1.Sum([]byte(fmt.Sprintf("%d %s https://gemini.google.com", ts, sapisid)))
	return fmt.Sprintf("SAPISIDHASH %d_%s", ts, hex.EncodeToString(h[:]))
}

// ChromeUA 是给 stdlib 走代理时用的 Chrome 146 真实 UA 模板。
const ChromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"

// applyChromeHeaders 给 stdlib 请求填上模拟 Chrome 146 的应用层 header。
// utls 那层做 TLS/HTTP2 指纹我们做不到（走代理），但 header 一定要齐。
func applyChromeHeaders(req *http.Request) {
	req.Header.Set("User-Agent", ChromeUA)
	req.Header.Set("Sec-CH-UA", `"Chromium";v="146", "Google Chrome";v="146", "Not?A_Brand";v="24"`)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
}
