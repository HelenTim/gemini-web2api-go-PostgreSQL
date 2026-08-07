package app

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

// 文件上传：两步 resumable，逐字对齐浏览器抓包。
//
// 之前按参考实现写过一版单次 multipart 打 content-push.googleapis.com/upload，
// 那条也能传成功，但**跟浏览器不是一回事**：域名不同、协议不同，而且返回的引用
// 路径短一截（浏览器那条带 `_Ad6Osdc…` 后缀）。附件能不能被对话引用是服务端说了算，
// 拿一条形状都不同的路径去赌不值当，所以改成照抄浏览器。
const uploadHost = "https://push.clients6.google.com/upload/"

// uploadBytes 上传一段字节，返回服务端给的引用路径（形如 /contrib_service/ttl_1d/…）。
//
// 注意上传请求里**不带 mime**：浏览器两步都用 application/x-www-form-urlencoded，
// 文件类型是在对话 payload 引用附件时才告诉服务端的。
//
// cookie 可以为空 —— 匿名也能传上去。但**匿名传上去的文件在对话里引用会被服务端
// 回 1100 拒绝**，所以调用方要自己确保有 cookie 才把引用填进 payload。
//
// proxyURL 必须跟正式请求同一个出口：文件在 A 出口上传、对话从 B 出口引用，
// 在 Google 眼里是两个会话在共用文件。
func uploadBytes(cookie, proxyURL string, data []byte, filename string) (string, error) {
	pushID, pctx, err := getUploadTokens(cookie, proxyURL)
	if err != nil {
		return "", fmt.Errorf("取上传页面参数失败: %w", err)
	}
	if pushID == "" {
		return "", fmt.Errorf("页面里没有 push_id，无法上传")
	}

	base := map[string]string{
		"Origin":         "https://gemini.google.com",
		"Referer":        "https://gemini.google.com/",
		"X-Tenant-Id":    "bard-storage",
		"Push-ID":        pushID,
		"Accept":         "*/*",
		"Sec-Fetch-Site": "same-site",
		"Sec-Fetch-Mode": "cors",
		"Sec-Fetch-Dest": "empty",
	}
	if pctx != "" {
		base["X-Client-Pctx"] = pctx
	}
	if cookie != "" {
		base["Cookie"] = cookie
	}
	with := func(extra map[string]string) map[string]string {
		h := make(map[string]string, len(base)+len(extra))
		for k, v := range base {
			h[k] = v
		}
		for k, v := range extra {
			h[k] = v
		}
		return h
	}

	// ── 第一步：start，拿一次性的上传 URL ──
	// body 是纯文本 "File name: xxx"，不是表单也不是 JSON，尽管 Content-Type
	// 写的是 urlencoded —— 抓包就是这么发的，别按 Content-Type 去猜。
	startHeaders := with(map[string]string{
		"Content-Type":                        "application/x-www-form-urlencoded;charset=UTF-8",
		"X-Goog-Upload-Command":               "start",
		"X-Goog-Upload-Protocol":              "resumable",
		"X-Goog-Upload-Header-Content-Length": strconv.Itoa(len(data)),
	})
	status, respHead, body, err := uploadPost(uploadHost, startHeaders,
		[]byte("File name: "+sanitizeUploadName(filename)), proxyURL)
	if err != nil {
		return "", fmt.Errorf("上传 start 失败: %w", err)
	}
	if status != 200 {
		return "", fmt.Errorf("上传 start 返回 HTTP %d: %s", status, truncate(string(body), 160))
	}
	putURL := respHead["x-goog-upload-url"]
	if putURL == "" {
		return "", fmt.Errorf("上传 start 没返回 x-goog-upload-url")
	}

	// ── 第二步：把字节发过去并 finalize ──
	upHeaders := with(map[string]string{
		"Content-Type":          "application/x-www-form-urlencoded;charset=utf-8",
		"X-Goog-Upload-Command": "upload, finalize",
		"X-Goog-Upload-Offset":  "0",
	})
	status, _, body, err = uploadPost(putURL, upHeaders, data, proxyURL)
	if err != nil {
		return "", fmt.Errorf("上传 finalize 失败: %w", err)
	}
	if status != 200 {
		return "", fmt.Errorf("上传 finalize 返回 HTTP %d: %s", status, truncate(string(body), 160))
	}
	// 成功时响应体就是一行引用路径。开头不是 "/" 说明拿到的是错误页之类，
	// 不能当引用往 payload 里填 —— 填了服务端未必报错，但模型看到的附件是空的。
	ref := strings.TrimSpace(string(body))
	if !strings.HasPrefix(ref, "/") {
		return "", fmt.Errorf("上传返回的不是引用路径: %s", truncate(ref, 160))
	}
	return ref, nil
}

// sanitizeUploadName 去掉会破坏请求头/体的字符。文件名会原样进 start 的 body。
func sanitizeUploadName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "upload.bin"
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(name)
}

// uploadPost 走跟主请求相同的出口发 POST，并把响应头一起带回来
// （start 那步要从响应头里取 x-goog-upload-url）。
func uploadPost(url string, headers map[string]string, body []byte, proxyURL string) (
	int, map[string]string, []byte, error) {
	if proxyURL != "" {
		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
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
		h := map[string]string{}
		for k := range resp.Header {
			h[strings.ToLower(k)] = resp.Header.Get(k)
		}
		return resp.StatusCode, h, b, err
	}
	req, err := fhttp.NewRequest("POST", url, bytes.NewReader(body))
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
	h := map[string]string{}
	for k := range resp.Header {
		h[strings.ToLower(k)] = resp.Header.Get(k)
	}
	return resp.StatusCode, h, b, err
}
