# gemini-web2api-go

Gemini 网页协议反代成 OpenAI 兼容 API（Go 重写版）。

- **Chrome 146 真指纹**（`tls-client` v1.14，TLS/HTTP2 fingerprint 跟 chat2api 同等级）
- **API Key 鉴权**（首次启动自动生成 `sk-gemini-*`，可在 admin 面板里改）
- **SQLite 持久化**（30 天请求明细 + 永久小时/天聚合）
- **管理面板**（中文 UI，单 HTML embed 进二进制；仪表盘 + 请求记录 + 代理池 + 设置）
- **代理池**（运行时增删改 + 失败 5 次自动熔断 + 轮询调度）
- **Docker 部署**（multi-stage → distroless，最小镜像；同 Pante docker network）
- **隐私**：只存元数据（模型/代理/延迟/token 数/状态），prompt 和 response 内容**永不入库**
- **Token 计算**：tiktoken cl100k_base BPE，中英文都比 chars/4 准；Gemini 自己不返回 token 数（实测）

## 快速跑

```bash
# 本地编译
go build -o gemini-web2api-go.exe .
./gemini-web2api-go.exe --port 8083 --admin-token your-admin-token

# Docker
docker compose up -d --build
```

启动后：
- OpenAI API: `http://localhost:8083/v1/chat/completions`
- 管理面板: `http://localhost:8083/admin`
- 默认 8083 端口

首次启动会在 banner 里看到自动生成的 API Key（`sk-gemini-*`），调用时 `Authorization: Bearer <key>` 或 `x-api-key: <key>` 二选一。

## 配置

`config.json`（可选，CLI flag 优先级更高）：

```json
{
  "port": 8083,
  "host": "0.0.0.0",
  "impersonate": "chrome_146",
  "db_path": "./data/gemini.db",
  "retention_days": 30,
  "admin_enabled": true,
  "admin_token": "",
  "request_timeout_sec": 180,
  "retry_attempts": 3,
  "default_model": "gemini-3.5-flash"
}
```

支持的 impersonate：`chrome_146` (默认) / `chrome_144` / `chrome_133` / `firefox_147` / `safari_16_0` / `safari_ios_17_0`

环境变量：
- `ADMIN_TOKEN` — 管理面板登录 token
- `API_KEY` — 锁定 API key（设了之后 admin UI 不能改，必须重启换值）

## 模型

| 模型 | 描述 |
|---|---|
| `gemini-3.5-flash` | 快速通用 |
| `gemini-3.5-flash-thinking` | 深度思考，输出最长 ~20k 字符 |
| `gemini-3.5-flash-thinking-lite` | 自适应思考深度 |
| `gemini-3.1-pro` | Pro 模型（要 cookie 才路由真 Pro） |
| `gemini-auto` | 自动选模型 |
| `gemini-flash-lite` | 轻量快速 |

模型名后加 `@think=N` 覆盖思考深度（0=最深，4=最浅）：
```
gemini-3.5-flash-thinking@think=2
```

## 文件

```
main.go              入口 + flag 解析 + 路由注册
config.go            配置 + JSON 加载
client.go            tls-client 缓存（per-proxy 实例）+ cookie + SAPISIDHASH
gemini.go            80 槽位 payload + StreamGenerate + wrb.fr 解析
messages.go          OpenAI messages → prompt + tool_call 解析
server.go            /v1/models + /v1/chat/completions + /v1/responses + metrics 写入
tokenizer.go         tiktoken cl100k_base 单例
apikey.go            /v1/* API key 鉴权（locked / runtime-mutable 双轨）
db.go                SQLite schema + sessions + requests + kv
proxy.go             代理池 CRUD + 轮询选择 + 熔断
scheduler.go         小时/天聚合 + 数据保留 + 代理池热加载
admin.go             /admin/api/* 鉴权 + stats/timeseries/requests/proxies/apikey
admin_ui.go          embed admin_ui/ 静态文件
admin_ui/index.html  单页应用（中文 UI + Chart.js CDN）
Dockerfile           multi-stage build (alpine → distroless)
docker-compose.yml   接 Pante network，sqlite volume 持久化
```

## 接进 Pante 体系

跟 cliproxy/chat2api 一样接进 newapi 当上游渠道：

- newapi 配渠道 base_url=`http://gemini-web2api:8083`，API key 填 admin 面板里那个 `sk-gemini-*`
- docker-compose 默认 `expose:` 不 `ports:` —— 不暴露公网，只让同 `pante` network 的容器访问
- 想本地调试取消 `ports: "127.0.0.1:8083:8083"` 注释

## 限制

- 匿名访问被 Google 按 IP 限流（几 RPS 量级）→ 配多代理池放大产能
- Pro 模型路由要 `--cookie-file cookie.txt`（free Google 账号即可，不要订阅）
- Tool calling 是 prompt 级实现（让模型输出 ```` ```tool_call``` ```` 块再 regex 解析），不是真协议层，模型偶尔不按格式返回

