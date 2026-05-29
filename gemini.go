package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/google/uuid"
)

// ModelConfig holds Gemini MODE_CATEGORY id + default thinking depth.
type ModelConfig struct {
	Mode  int
	Think int
	Desc  string
}

var Models = map[string]ModelConfig{
	"gemini-3.5-flash":                 {Mode: 1, Think: 4, Desc: "Fast general-purpose model"},
	"gemini-3.5-flash-thinking":        {Mode: 2, Think: 0, Desc: "Deep thinking mode, longest output (~20k chars)"},
	"gemini-3.1-pro":                   {Mode: 3, Think: 4, Desc: "Pro model (requires cookie for real routing)"},
	"gemini-auto":                      {Mode: 4, Think: 4, Desc: "Auto model selection"},
	"gemini-3.5-flash-thinking-lite":   {Mode: 5, Think: 0, Desc: "Dynamic thinking with adaptive depth"},
	"gemini-flash-lite":                {Mode: 6, Think: 4, Desc: "Lightweight fast model"},
}

// resolveModel parses "name@think=N" and returns base name, mode id, think depth.
func resolveModel(modelName string) (string, int, int, error) {
	thinkOverride := -1
	if idx := strings.Index(modelName, "@think="); idx >= 0 {
		fmt.Sscanf(modelName[idx+len("@think="):], "%d", &thinkOverride)
		modelName = modelName[:idx]
	}
	mc, ok := Models[modelName]
	if !ok {
		return "", 0, 0, fmt.Errorf("unknown model: %s", modelName)
	}
	think := mc.Think
	if thinkOverride >= 0 {
		think = thinkOverride
	}
	return modelName, mc.Mode, think, nil
}

// StreamResult holds raw body + per-request proxy + timing info.
type StreamResult struct {
	Raw       string
	ProxyID   int64
	ProxyName string
	TTFBMs    int64
	TotalMs   int64
}

// RateLimitError 表示所有 IP slot 都达到了限流上限。
// HTTP handler 看到这个错时返回 429 给客户端。
type RateLimitError struct {
	Reason   string // "concurrent" / "rpm" / "rph"
	ProxyID  int64  // 0 = 直连 slot 满
}

func (e *RateLimitError) Error() string {
	if e.ProxyID == 0 {
		return "direct IP slot full: " + e.Reason + " limit reached (configure proxies to scale)"
	}
	return "all proxy slots full: " + e.Reason + " limit reached"
}

// acquireSlot 选一个有容量的 slot 给本次请求用。
// 优先级：代理池里有容量的代理 → 直连。
// 全满返回 *RateLimitError。
//
// 调用方拿到 (proxy, ok=true) 必须配 deferred releaseSlot()。
func acquireSlot() (Proxy, bool, error) {
	// 1. 先试代理池（如果配了）
	proxyMu.RLock()
	hasProxies := len(proxyCache) > 0
	proxyMu.RUnlock()

	if hasProxies {
		if p, ok := pickProxyWithCapacity(); ok {
			return p, true, nil
		}
		// 代理池存在但都满了 → 不退回直连（因为部署者明确想用代理）
		return Proxy{}, false, &RateLimitError{Reason: "rph", ProxyID: -1}
	}

	// 2. 没配代理池 → 用直连 slot（id=0）
	if ok, reason := trySlotAcquire(0); ok {
		return Proxy{}, true, nil // ProxyID=0 表示直连
	} else {
		return Proxy{}, false, &RateLimitError{Reason: reason, ProxyID: 0}
	}
}

// releaseSlot 释放占用。proxyID=0 表示直连。
func releaseSlot(proxyID int64) {
	slotRelease(proxyID)
}

// streamGenerate POSTs to Gemini's StreamGenerate endpoint and returns raw body
// plus proxy/timing telemetry for the metrics layer.
// The 80-slot inner array is verbatim from the Python reference.
func streamGenerate(prompt string, modelID, thinkMode int) (*StreamResult, error) {
	inner := make([]interface{}, 80)
	inner[0] = []interface{}{prompt, 0, nil, nil, nil, nil, 0}
	inner[1] = []interface{}{"en"}
	inner[2] = []interface{}{"", "", "", nil, nil, nil, nil, nil, nil, ""}
	inner[6] = []interface{}{0}
	inner[7] = 1
	inner[10] = 1
	inner[11] = 0
	inner[17] = []interface{}{[]interface{}{thinkMode}}
	inner[18] = 0
	inner[27] = 1
	inner[30] = []interface{}{4}
	inner[41] = []interface{}{2}
	inner[53] = 0
	inner[59] = uuid.NewString()
	inner[61] = []interface{}{}
	inner[68] = 1
	inner[79] = modelID

	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}
	outer := []interface{}{nil, string(innerJSON)}
	outerJSON, err := json.Marshal(outer)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("f.req", string(outerJSON))
	body := form.Encode()

	reqid := time.Now().Unix() % 1000000
	endpoint := fmt.Sprintf(
		"https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?bl=%s&hl=en&_reqid=%d&rt=c",
		cfg.GeminiBL, reqid,
	)

	cookieStr, sapisid := loadCookie()

	// 通过限流器拿一个 slot（代理或直连）。所有 slot 满 → 直接 429。
	picked, slotOK, slotErr := acquireSlot()
	if !slotOK {
		return nil, slotErr
	}
	defer releaseSlot(picked.ID) // picked.ID=0 表示直连 slot

	proxyURL := picked.URL
	if proxyURL == "" {
		proxyURL = cfg.Proxy // fallback 静态 proxy（一般用不到）
	}
	pickedOK := picked.ID > 0 // 是否真用了代理池里的代理

	geminiHeaders := buildGeminiHeaders(cookieStr, sapisid)
	var lastErr error
	t0 := time.Now()
	for attempt := 0; attempt < cfg.RetryAttempts; attempt++ {
		sendT := time.Now()
		statusCode, raw, err := doGeminiRequest(endpoint, body, geminiHeaders, proxyURL)
		if err != nil {
			lastErr = err
			if pickedOK {
				recordProxyResult(picked.ID, false, err.Error())
			}
			if attempt < cfg.RetryAttempts-1 {
				logf("retry %d/%d: %v", attempt+1, cfg.RetryAttempts, err)
				time.Sleep(time.Duration(cfg.RetryDelaySec) * time.Second)
			}
			continue
		}
		ttfb := time.Since(sendT).Milliseconds()
		if statusCode != 200 {
			lastErr = fmt.Errorf("upstream HTTP %d: %s", statusCode, truncate(string(raw), 200))
			if pickedOK {
				recordProxyResult(picked.ID, false, lastErr.Error())
			}
			if attempt < cfg.RetryAttempts-1 {
				time.Sleep(time.Duration(cfg.RetryDelaySec) * time.Second)
			}
			continue
		}
		if pickedOK {
			recordProxyResult(picked.ID, true, "")
		}
		return &StreamResult{
			Raw:       string(raw),
			ProxyID:   picked.ID,
			ProxyName: picked.Name,
			TTFBMs:    ttfb,
			TotalMs:   time.Since(t0).Milliseconds(),
		}, nil
	}
	return nil, lastErr
}

// buildGeminiHeaders 准备 StreamGenerate 必需的应用层 header。
func buildGeminiHeaders(cookieStr, sapisid string) map[string]string {
	h := map[string]string{
		"Accept":            "*/*",
		"Accept-Language":   "en-US,en;q=0.9",
		"Content-Type":      "application/x-www-form-urlencoded;charset=UTF-8",
		"Origin":            "https://gemini.google.com",
		"Referer":           "https://gemini.google.com/app",
		"X-Same-Domain":     "1",
		"X-Goog-AuthUser":   "0",
	}
	if cookieStr != "" {
		h["Cookie"] = cookieStr
	}
	if sapisid != "" {
		h["Authorization"] = makeSAPISIDHash(sapisid)
	}
	return h
}

// doGeminiRequest 发一次请求到 endpoint。proxyURL 非空走 stdlib（支持 socks5/http），
// 空走 tls-client（chrome146 真指纹）。返回 (HTTP status, body bytes, err)。
func doGeminiRequest(endpoint, body string, headers map[string]string, proxyURL string) (int, []byte, error) {
	if proxyURL != "" {
		// 走 stdlib —— 跟 Kiro-Gogogo 同款 http.ProxyURL 实现，已知能过 socks5/socks5h。
		req, err := http.NewRequest("POST", endpoint, strings.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		applyChromeHeaders(req)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		client := getStdlibClient(proxyURL)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, nil, err
		}
		return resp.StatusCode, raw, nil
	}

	// 直连 → tls-client，保留 chrome146 TLS/HTTP2 真指纹
	req, err := fhttp.NewRequest("POST", endpoint, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := getTLSClient()
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

// extractResponseText parses StreamGenerate's wrb.fr stream and returns the
// last non-empty text chunk (matches Python extract_response_text behavior).
func extractResponseText(raw string) string {
	var texts []string
	for _, line := range strings.Split(raw, "\n") {
		if !strings.Contains(line, `"wrb.fr"`) || len(line) < 200 {
			continue
		}
		var arr []interface{}
		if err := json.Unmarshal([]byte(line), &arr); err != nil {
			continue
		}
		if len(arr) == 0 {
			continue
		}
		first, ok := arr[0].([]interface{})
		if !ok || len(first) < 3 {
			continue
		}
		innerStr, ok := first[2].(string)
		if !ok || len(innerStr) < 50 {
			continue
		}
		var inner []interface{}
		if err := json.Unmarshal([]byte(innerStr), &inner); err != nil {
			continue
		}
		if len(inner) <= 4 {
			continue
		}
		parts, ok := inner[4].([]interface{})
		if !ok {
			continue
		}
		for _, p := range parts {
			pl, ok := p.([]interface{})
			if !ok || len(pl) < 2 {
				continue
			}
			tl, ok := pl[1].([]interface{})
			if !ok {
				continue
			}
			for _, t := range tl {
				if s, ok := t.(string); ok && s != "" {
					texts = append(texts, s)
				}
			}
		}
	}
	for i := len(texts) - 1; i >= 0; i-- {
		if strings.TrimSpace(texts[i]) != "" {
			return cleanGeminiText(texts[i])
		}
	}
	return ""
}

var codeArtifactRe = regexp.MustCompile("(?s)```(?:python|javascript|text)\\?code_(?:reference|stdout)&code_event_index=\\d+\\n.*?```\\n?")

func cleanGeminiText(text string) string {
	return strings.TrimSpace(codeArtifactRe.ReplaceAllString(text, ""))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ProbeResult 是 admin 测试接口返回的连通性诊断结果。
type ProbeResult struct {
	OK            bool   `json:"ok"`
	Status        string `json:"status"`         // "success" / "blocked_sorry" / "rate_limited" / "upstream_error" / "network_error"
	HTTPCode      int    `json:"http_code"`
	TotalMs       int64  `json:"total_ms"`
	ProxyID       int64  `json:"proxy_id"`
	ProxyName     string `json:"proxy_name"`
	UseDirect     bool   `json:"use_direct"`
	ResponseText  string `json:"response_text"`  // 截断到 200 字符
	UpstreamSnip  string `json:"upstream_snip"`  // 上游原始响应前 300 字符
	Diagnostic    string `json:"diagnostic"`     // 中文诊断说明
	Impersonate   string `json:"impersonate"`
}

// probeGemini 直接调 Gemini StreamGenerate（绕过限流），返回详细诊断。
// 不写 db、不消耗限流 slot。
func probeGemini(prompt, proxyURL string) ProbeResult {
	res := ProbeResult{Impersonate: cfg.Impersonate}

	inner := make([]interface{}, 80)
	inner[0] = []interface{}{prompt, 0, nil, nil, nil, nil, 0}
	inner[1] = []interface{}{"en"}
	inner[2] = []interface{}{"", "", "", nil, nil, nil, nil, nil, nil, ""}
	inner[6] = []interface{}{0}
	inner[7] = 1
	inner[10] = 1
	inner[11] = 0
	inner[17] = []interface{}{[]interface{}{4}}
	inner[18] = 0
	inner[27] = 1
	inner[30] = []interface{}{4}
	inner[41] = []interface{}{2}
	inner[53] = 0
	inner[59] = uuid.NewString()
	inner[61] = []interface{}{}
	inner[68] = 1
	inner[79] = 1 // FAST 模式（mode=1 = gemini-3.5-flash）

	innerJSON, _ := json.Marshal(inner)
	outer := []interface{}{nil, string(innerJSON)}
	outerJSON, _ := json.Marshal(outer)
	form := url.Values{}
	form.Set("f.req", string(outerJSON))
	body := form.Encode()

	reqid := time.Now().Unix() % 1000000
	endpoint := fmt.Sprintf(
		"https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?bl=%s&hl=en&_reqid=%d&rt=c",
		cfg.GeminiBL, reqid,
	)

	cookieStr, sapisid := loadCookie()
	headers := buildGeminiHeaders(cookieStr, sapisid)

	// 复用主流程同款 client 选择规则:有代理走 stdlib，没代理走 tls-client。
	// 但 probe 需要看 302 的 Location header,所以这里直接发不用 doGeminiRequest。
	var statusCode int
	var raw []byte
	var locHeader string
	var err error

	if proxyURL != "" {
		req, e := http.NewRequest("POST", endpoint, strings.NewReader(body))
		if e != nil {
			res.Status = "network_error"
			res.Diagnostic = "构建请求失败: " + e.Error()
			return res
		}
		applyChromeHeaders(req)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		client := getStdlibClient(proxyURL)
		resp, e := client.Do(req)
		if e != nil {
			res.Status = "network_error"
			res.Diagnostic = "网络层错误（DNS/TCP/TLS 失败）: " + e.Error()
			return res
		}
		defer resp.Body.Close()
		statusCode = resp.StatusCode
		locHeader = resp.Header.Get("Location")
		raw, err = io.ReadAll(resp.Body)
	} else {
		req, e := fhttp.NewRequest("POST", endpoint, strings.NewReader(body))
		if e != nil {
			res.Status = "network_error"
			res.Diagnostic = "构建请求失败: " + e.Error()
			return res
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		client := getTLSClient()
		resp, e := client.Do(req)
		if e != nil {
			res.Status = "network_error"
			res.Diagnostic = "网络层错误（DNS/TCP/TLS 失败）: " + e.Error()
			return res
		}
		defer resp.Body.Close()
		statusCode = resp.StatusCode
		locHeader = resp.Header.Get("Location")
		raw, err = io.ReadAll(resp.Body)
	}
	_ = err
	res.HTTPCode = statusCode
	res.UpstreamSnip = truncate(string(raw), 300)

	switch {
	case statusCode == 302:
		res.Status = "blocked_sorry"
		res.Diagnostic = "IP 被 Google 风控（重定向到 sorry/index）。" +
			"通常 6-24 小时解除，或换 VPN/代理 IP 立即恢复。Location: " + truncate(locHeader, 200)
		return res
	case statusCode == 429:
		res.Status = "rate_limited"
		res.Diagnostic = "Google 直接返回 429 限流。同样是 IP 嫌疑，但风控分支不同（朴素 SDK 路径）。"
		return res
	case statusCode != 200:
		res.Status = "upstream_error"
		res.Diagnostic = fmt.Sprintf("上游返回非 200 (HTTP %d)，可能是协议变更或临时故障。", statusCode)
		return res
	}

	text := extractResponseText(string(raw))
	if text == "" {
		res.Status = "upstream_error"
		res.Diagnostic = "上游 200 但解析不到回复文本，可能 wrb.fr 帧格式变了或被空响应。"
		return res
	}

	res.OK = true
	res.Status = "success"
	res.ResponseText = truncate(text, 200)
	res.Diagnostic = "调用成功。延迟 / 内容见上面字段。"
	return res
}
