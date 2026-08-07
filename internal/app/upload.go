package app

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

// 文件上传：把字节传到 content-push，拿回一个能填进 payload 的引用路径。
//
// 这是**单次 multipart POST**，不是我们早期文档里记的两步 resumable。
// 两步那套（X-Goog-Upload-Protocol: resumable，先 start 拿 upload URL 再 upload+finalize）
// 也能通，但多一次往返；浏览器和参考实现走的都是这条单次的。
const uploadEndpoint = "https://content-push.googleapis.com/upload"

// uploadBytes 上传一段字节，返回服务端给的引用路径（形如 /contrib_service/ttl_1d/xxxx）。
//
// cookie 可以为空 —— 匿名也能传上去。但**匿名传上去的文件在对话里引用会被服务端
// 回 1100 拒绝**，所以调用方要自己确保有 cookie 才把引用填进 payload。
//
// proxyURL 必须跟正式请求同一个出口：文件在 A 出口上传、对话从 B 出口引用，
// 在 Google 眼里是两个会话在共用文件。
func uploadBytes(cookie, proxyURL string, data []byte, filename, mimeType string) (string, error) {
	pushID, pctx, err := getUploadTokens(cookie, proxyURL)
	if err != nil {
		return "", fmt.Errorf("取上传页面参数失败: %w", err)
	}
	if pushID == "" {
		return "", fmt.Errorf("页面里没有 push_id，无法上传")
	}

	body, contentType, err := buildUploadBody(data, filename, mimeType)
	if err != nil {
		return "", err
	}
	headers := map[string]string{
		"Origin":       "https://gemini.google.com",
		"Referer":      "https://gemini.google.com/",
		"X-Tenant-Id":  "bard-storage",
		"Push-ID":      pushID,
		"Content-Type": contentType,
	}
	// pctx 抓不到也照发：参考实现只带 Push-ID 就能传成功，它更像是锦上添花。
	// 但带上更贴近浏览器，没理由故意少发。
	if pctx != "" {
		headers["X-Client-Pctx"] = pctx
	}
	if cookie != "" {
		headers["Cookie"] = cookie
	}

	status, resp, err := postUpload(body, headers, proxyURL)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fmt.Errorf("上传返回 HTTP %d: %s", status, truncate(string(resp), 160))
	}
	// 成功时响应体就是一行引用路径。开头不是 "/" 说明拿到的是错误页之类，
	// 不能当引用往 payload 里填 —— 填了会变成一个语义不明的字符串，
	// 服务端未必报错，但模型看到的附件是空的。
	ref := strings.TrimSpace(string(resp))
	if !strings.HasPrefix(ref, "/") {
		return "", fmt.Errorf("上传返回的不是引用路径: %s", truncate(ref, 160))
	}
	return ref, nil
}

// buildUploadBody 拼 multipart 表单，字段名固定 "file"。
func buildUploadBody(data []byte, filename, mimeType string) ([]byte, string, error) {
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	// 自己拼 Content-Disposition 而不用 CreateFormFile：后者会把 mime 写死成
	// application/octet-stream，而服务端按这个 mime 决定怎么处理附件。
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="file"; filename=%q`, sanitizeUploadName(filename)))
	h.Set("Content-Type", mimeType)
	part, err := w.CreatePart(h)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// sanitizeUploadName 去掉会破坏 Content-Disposition 的字符。
func sanitizeUploadName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "upload.bin"
	}
	repl := strings.NewReplacer("\r", "", "\n", "", `"`, "_", `\`, "_", "/", "_")
	return repl.Replace(name)
}

// postUpload 走跟主请求相同的出口发 POST：配了代理走 stdlib，没配走 tls-client。
func postUpload(body []byte, headers map[string]string, proxyURL string) (int, []byte, error) {
	if proxyURL != "" {
		req, err := http.NewRequest("POST", uploadEndpoint, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		applyChromeHeaders(req)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := getStdlibClient(proxyURL).Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return resp.StatusCode, b, err
	}
	req, err := fhttp.NewRequest("POST", uploadEndpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := getTLSClient().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}
