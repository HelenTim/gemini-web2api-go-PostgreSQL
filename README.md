# gemini-web2api-go

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8.svg)](https://golang.org)
[![Docker](https://img.shields.io/badge/docker-distroless-blue)](Dockerfile)

中文 | [English](README_EN.md)

把 Google Gemini 网页端反代成 OpenAI 兼容 API。**单二进制**，**零账号**（匿名可跑，挂 Google cookie 可解锁 Pro），**Chrome 146 真指纹**，**SQLite 持久化**，自带**中文管理面板**。

> 协议层逐字段对齐了一份社区的 Python 单文件参考实现（也叫 `gemini-web2api`，stdlib only），等价性已验证。

---

## 这是什么

把这种调用：
```
[OpenAI SDK / Cherry Studio / Cursor / dify / newapi / ...]
    ↓ http://localhost:8083/v1/chat/completions
[gemini-web2api-go]
    ↓ 逆向 gemini.google.com 网页协议
[Google Gemini 网页端]
```

不是 Google 官方 API（[generativelanguage.googleapis.com](https://generativelanguage.googleapis.com)）的二次封装——**直接反代浏览器协议**，所以**不需要 Google API Key、不需要付费配额**。

## 跟参考实现的区别

社区有一份单文件 Python 参考实现（同名 `gemini-web2api`，stdlib only）。本项目协议层照搬，但工程能力差异较大：

| 维度 | Python 单文件版 | gemini-web2api-go |
|---|---|---|
| 部署 | `python gemini_web2api.py` | 单二进制（~25MB Docker 镜像） |
| 依赖 | stdlib only | 编译产物零外部依赖 |
| 指纹 | urllib 默认（Google 视作 SDK） | **utls Chrome 146**（视作浏览器） |
| API 鉴权 | ❌ 裸奔 | ✅ Bearer token / x-api-key |
| 持久化 | ❌ 无 | ✅ SQLite，30 天明细 + 永久聚合 |
| 管理面板 | ❌ | ✅ 中文 Web UI（仪表盘 / 请求记录 / 代理池 / 设置） |
| 限流保护 | ❌ | ✅ 每 IP slot 独立并发/RPM/RPH 限额 |
| 代理池 | 单一静态代理 | ✅ 运行时增删改 + 失败熔断 + 轮询调度 |
| 隐私 | n/a | ✅ Prompt/Response 内容**永不入库**，只存元数据 |

## 快速开始

### 编译

```bash
go build -o gemini-web2api-go .
./gemini-web2api-go --port 8083 --admin-token your-admin-token
```

### Docker

```bash
docker compose up -d --build
```

启动后会看到 banner：

```
gemini-web2api-go v3.0.0
  Listening:   http://0.0.0.0:8083
  Base URL:    http://localhost:8083/v1
  API key:     sk-gemini-XX...XXXX  (mutable in admin UI)
  Admin UI:    http://localhost:8083/admin  (token auth)
  Impersonate: chrome_146
  Tokenizer:   tiktoken cl100k_base
  Per-IP 限流: 并发=5 / RPM=30 / RPH=80
```

### 调用

```bash
curl http://localhost:8083/v1/chat/completions \
  -H "Authorization: Bearer sk-gemini-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.5-flash",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

OpenAI Python SDK 也直接能用：

```python
from openai import OpenAI
client = OpenAI(
    base_url="http://localhost:8083/v1",
    api_key="sk-gemini-..."  # admin 面板里看
)
resp = client.chat.completions.create(
    model="gemini-3.5-flash-thinking",
    messages=[{"role": "user", "content": "解释量子纠缠"}]
)
print(resp.choices[0].message.content)
```

## 管理面板

`http://localhost:8083/admin`，用 `--admin-token` 登录。

- **仪表盘** — 24h KPI + 请求量/P50 延迟双轴趋势图 + 模型/代理分组统计 + IP 限流用量 + 一键连通性诊断
- **请求记录** — 明细列表（仅元数据，无 prompt/response 内容），状态/模型筛选 + 分页
- **代理池** — 运行时增删改 + 启用/禁用 + 失败次数熔断（每代理是独立 IP slot）
- **设置** — API Key 显示/复制/旋转/自定义 + 限流配置查看

## 模型

| 模型 | 描述 | 输出长度 |
|---|---|---|
| `gemini-3.5-flash` | 快速通用 | ~12k 字符 |
| `gemini-3.5-flash-thinking` | 深度思考，最长输出 | **~20k 字符** |
| `gemini-3.5-flash-thinking-lite` | 自适应思考深度 | ~15k 字符 |
| `gemini-3.1-pro` | Pro 模型（需 cookie 才走真 Pro 路由）| ~12k 字符 |
| `gemini-auto` | 自动选模型 | varies |
| `gemini-flash-lite` | 轻量极速 | ~10k 字符 |

模型名后加 `@think=N` 可覆盖思考深度（`0` 最深 ↔ `4` 最浅）：

```
gemini-3.5-flash-thinking@think=2
```

## 配置

`config.json`（可选，CLI flag 优先）：

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
  "default_model": "gemini-3.5-flash",
  "per_ip_concurrent": 5,
  "per_ip_rpm": 30,
  "per_ip_rph": 80
}
```

支持的 impersonate：`chrome_146`（默认）/ `chrome_144` / `chrome_133` / `firefox_147` / `safari_16_0` / `safari_ios_17_0`

环境变量：
- `ADMIN_TOKEN` — 管理面板登录 token
- `API_KEY` — 锁定 `/v1/*` API key（不会显示在 admin UI 的"自定义"按钮里）

## Pro 模型 (可选)

匿名可调所有模型，但 `gemini-3.1-pro` 不挂 cookie 会被路由回 Flash。挂一个 free Google 账号 cookie 即可走真 Pro 路由（不需要付费订阅）：

1. 浏览器登录 [gemini.google.com](https://gemini.google.com)
2. DevTools (F12) → Application → Cookies → `https://gemini.google.com`
3. 复制：`SID` / `HSID` / `SSID` / `APISID` / `SAPISID` / `__Secure-1PSID`
4. `cookie.txt`：
```
SID=...; HSID=...; SSID=...; APISID=...; SAPISID=...; __Secure-1PSID=...
```
5. 启动加 `--cookie-file cookie.txt`

## 代理池（白嫖路线核心）

**为什么需要**：实测单 IP 累积 **~100 次** Gemini 网页请求就会被重定向到 `google.com/sorry/index`，所有后续请求 **6-24 小时**内全部失败。

**怎么解决**：在管理面板「代理池」页面加多个代理，**每个代理是一个独立的 IP slot**，享有独立的并发/RPM/RPH 配额。N 个代理 = N 倍总容量。

支持的代理协议：
- `http://user:pass@host:port`
- `https://user:pass@host:port`
- `socks5://user:pass@host:port`
- `socks5h://user:pass@host:port`（远程 DNS 解析，绕开本地 DNS 污染）

**自动调度规则**：
- 配了代理后，**不会再退回直连**（避免代理满了把主机 IP 也打爆）
- 失败 5 次自动熔断（管理面板可手动重置）
- 全部代理满 → 返回 HTTP 429（不消耗 Google 配额，等空位再重试）

## 指纹模拟

直连场景（无代理）走 [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client)，TLS 握手 + HTTP/2 SETTINGS 帧 + ALPS 全部对齐真实 Chrome 146。Google 风控视角下，跟真浏览器无法区分。

走代理场景换用 stdlib `net/http` + `http.ProxyURL`（兼容性最佳），但应用层 header（`Sec-CH-UA` / `Sec-Fetch-*` / `User-Agent`）仍按 Chrome 146 真实值伪装。

**实测意外**：朴素 SDK 调用（如 Python urllib）触发风控时拿到 **HTTP 429**；伪装成 Chrome 后触发风控拿到 **HTTP 302** 跳转到 `google.com/sorry/index`（CAPTCHA 验证页）。两者本质都是 IP 黑名单，但 302 证明 Google 真的把我们认成了浏览器。

## 隐私

- **Prompt 和 response 内容永不入库**——只存元数据：模型、代理 ID、延迟、token 数、状态码、错误类型
- 历史 `prompt_preview` / `response_preview` 列从老版本迁移时自动 DROP
- Token 数用 [tiktoken-go](https://github.com/pkoukk/tiktoken-go) cl100k_base BPE 分词器精确计算（中英文都准），不是 `len/4` 估算
- Gemini 网页端不返回真 token 数（实测响应里没有任何 token/usage 字段，只能本地估算）

## OpenAI 协议覆盖

| 路径 | 状态 | 备注 |
|---|---|---|
| `POST /v1/chat/completions` | ✅ | 含 `stream: true` SSE |
| `POST /v1/responses` | ✅ | OpenAI Responses API（Codex CLI 用） |
| `GET /v1/models` | ✅ | 列出 6 个模型 |
| Function calling | ⚠️ | Prompt 级实现（让模型输出 ` ```tool_call``` ` 块再 regex 解析），不是真协议层。模型偶尔不按格式返回 |
| Vision / 图片输入 | ❌ | 网页协议不支持文件上传 |
| Audio | ❌ | 网页协议不支持 |

## 项目结构

```
main.go              入口 + flag 解析 + 路由注册
config.go            配置加载 + DEFAULT_CONFIG
client.go            tls-client (chrome146) + stdlib (代理) 双 client
gemini.go            80 槽位 payload + StreamGenerate + wrb.fr 流解析
messages.go          OpenAI messages → prompt + tool_call 解析
server.go            /v1/* + 限流入口 + metrics 写入
ratelimit.go         每 IP slot 独立并发/RPM/RPH 限流器
tokenizer.go         tiktoken cl100k_base 单例
apikey.go            API key 管理（locked / runtime-mutable 双轨）
db.go                SQLite schema + sessions + requests + kv
proxy.go             代理池 CRUD + 容量调度 + 熔断
scheduler.go         小时/天聚合 + 数据保留
admin.go             /admin/api/* 鉴权 + REST 接口
admin_ui.go          embed admin_ui/ 静态文件
admin_ui/index.html  单页中文 admin（Chart.js CDN）
Dockerfile           multi-stage (alpine builder → distroless runtime)
docker-compose.yml   单容器,sqlite volume,本地 8083 暴露
```

## 限制

- **匿名访问**：单 IP 受 Google 限流，约 100 次/几小时被拦 6-24 小时 → 配代理池放大产能
- **Pro 路由**：要 free Google 账号 cookie，不需要付费订阅
- **Function calling**：prompt 级实现，模型不一定每次都按格式返回（OpenAI 真协议层我们做不到）
- **多模态**：暂不支持（网页协议本身限制）
- **token 数**：用 tiktoken 估算（Gemini 真 tokenizer 未公开），跟真值偏差 ±20% 以内

## 致谢

- 社区 Python 单文件参考实现（同名 `gemini-web2api`）—— 协议层（80 槽位 payload、wrb.fr 解析、模型 ID 映射）的参考来源
- [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client) — Chrome 真指纹 TLS 库
- [pkoukk/tiktoken-go](https://github.com/pkoukk/tiktoken-go) — BPE tokenizer
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — 纯 Go SQLite（CGO-free，alpine 直接编）

## License

MIT — 详见 [LICENSE](LICENSE)
