# gemini-web2api-go

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8.svg)](https://golang.org)
[![Docker](https://img.shields.io/badge/docker-distroless-blue)](Dockerfile)

中文 | [English](README_EN.md)

把 Google Gemini 网页端反代成 OpenAI 兼容 API。**单二进制**，**零账号**（匿名可跑），**Chrome 146 真指纹**，**SQLite 持久化**，自带**中文管理面板**。

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
    "model": "gemini-3.6-flash",
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
    model="gemini-3.6-flash",
    messages=[{"role": "user", "content": "解释量子纠缠"}]
)
print(resp.choices[0].message.content)
```

## 管理面板

`http://localhost:8083/admin`，用 `--admin-token` 登录。

- **仪表盘** — 24h KPI + 请求量/P50 延迟双轴趋势图 + 模型/代理分组统计 + IP 限流用量 + 一键连通性诊断
- **请求记录** — 明细列表（仅元数据，无 prompt/response 内容），状态/模型筛选 + 分页
- **代理池** — 运行时增删改 + 启用/禁用 + 失败次数熔断（每代理是独立 IP slot）
- **设置** — 运行时配置表单（保存即生效）+ API Key 轮换 + 部署期配置只读展示

## 模型

Gemini 网页端服务端只认三个模型（清单来自 `batchexecute?rpcids=otAQ7b`）：

| 模型 | 描述 |
|---|---|
| `gemini-3.6-flash` | 全方位，默认 |
| `gemini-3.5-flash-lite` | 极速、轻量 |
| `gemini-3.1-pro` | **只在配了 `--cookie-file` 时才暴露**，见下 |

没配 cookie 时 `/v1/models` 只返回前两个，选 `gemini-3.1-pro` 会直接报错并说明
原因。因为匿名请求它必然被静默降级成 3.5 Flash-Lite——与其让客户端拿到一个
"成功但其实不是 Pro"的回复，不如在选型时就失败。

只暴露这三个。旧的 `gemini-3.5-flash`、`gemini-3.5-flash-thinking`、
`gemini-3.5-flash-thinking-lite`、`gemini-auto`、`gemini-flash-lite` **已移除**
（传了会返回 400）——它们在服务端没有对应条目，留着只会让人以为有五种不同
的模型可选。

> **`@think=N` 已废弃。** 该后缀写进请求的 `inner[17]`，一直被当作"思考深度"，
> 但抓包证明它是**会话内的轮次索引**（首轮 `[[0]]`，带会话 id 的第二轮 `[[1]]`，
> 逐轮递增），跟思考深度无关。我们每次都开新会话，该值恒为 0，所以这个参数
> 从来没有生效过。后缀仍被接受但直接忽略，不影响路由。

### 已知的能力边界

匿名调用（不挂 cookie）只能拿到上面两个文本模型 + Gemini 自带的联网搜索。
生图、音乐、视频、深度研究、画布、扩展思考都需要登录，匿名请求会被拒或降级
成普通文本。`gemini-3.1-pro` 拿不到：**匿名时被降级成 3.5 Flash-Lite，挂免费账号 cookie
后降级成「3.6 Flash 扩展」**，付费订阅未验证。管理面板的「实际模型」列会把这类
降级标出来。

**多轮上下文是靠把 `messages` 拼成单个 prompt 实现的，不是协议级多轮。**

Gemini 网页端本身支持协议级多轮（浏览器发第二句时只传新消息 + 会话 id，历史
由服务端保存），匿名会话也支持。但复现不了：按浏览器的确切格式传
`inner[2] = [cid, 上轮rid, "", …, token]`、`inner[17] = [[轮次]]`、URL 带
`f.sid`，全部对齐后服务端仍然拒绝。唯一没能复现的是 `inner[3]` 的 botguard
token——抓包里三轮分别是 1404 / 1847 / 2489 字节，由浏览器 JS 运行时生成，
纯 HTTP 客户端造不出来。

所以拼 prompt 是目前唯一可行的方式。代价是每轮重发全部历史，且受单次输入
长度上限约束。

## 配置

配置只有两个地方，按"改了要不要重启"分：

### 运行时配置 → 管理面板「设置」页

保存**立刻生效**，不用重启。存在数据库里，优先级高于 `config.json` 和命令行参数。

| 项 | 说明 |
|---|---|
| 默认模型 | 客户端没传 `model` 时用哪个 |
| 每 slot 并发 / RPM / RPH | 限流额度，0 = 不限 |
| 重试次数 / 重试间隔 / 上游超时 | |
| 明细保留天数 | 过期只删明细，聚合数据永久保留 |
| TLS 指纹 | `chrome_146`（默认）/ `chrome_144` / `chrome_133` / `firefox_147` / `safari_16_0` / `safari_ios_17_0` |
| Gemini `bl` 版本 | 上游前端版本号，过期时改这里 |
| 静态代理 | 代理池为空时的兜底；一般用「代理池」页面配 |
| 打印请求日志 | |

所有值都在后端做范围校验（比如 `retry_attempts` 只接受 1-10、超时 5-600 秒），
非法值会被拒绝并说明原因——浏览器端的限制随手就能绕过，真正的关卡在服务端。

### 凭证 → 也在面板

| 项 | 说明 |
|---|---|
| API Key | 首次启动自动生成，面板里可轮换或自定义 |
| Google Cookie | 面板「设置」页直接粘贴，保存即生效。挂上之后 `gemini-3.1-pro` 才会出现在模型列表里 |

两者都存在数据库里。已保存的值不回显（cookie 只显示识别到几个、关键项齐不齐）。

### 部署期配置 → `docker-compose.yml`

只剩改了必须重启进程的：

| 项 | 位置 |
|---|---|
| 监听端口 | `ports` + `command: --port` |
| 数据库路径 | `volumes` + `command: --db` |
| `ADMIN_TOKEN` | `environment`，面板登录 token |

另有两个**可选的锁定开关**，用于不希望运行时被改的部署：`API_KEY` 环境变量会
锁死 API key（面板改不了）、`--cookie-file` 指向的文件在面板没存 cookie 时作为
兜底。不设的话默认路径都是面板。

命令行参数仍然可用，定位是本地调试时的临时覆盖。优先级：
**面板改动 > CLI flag / `config.json` > 内置默认**。

## Cookie（可选）

挂 Google 账号 cookie 后请求会走登录态。**但 `gemini-3.1-pro` 拿不到**——实测
免费账号即使登录，请求 Pro 的模型 id 仍会被降级，服务端回报的是 3.6 Flash；
付费订阅账号未验证。

登录态在网页端能解锁生图（Nano Banana 2）、音乐（Lyria 3）、视频、深度研究、
画布、扩展思考，但**本项目尚未实现这些**，挂 cookie 目前只影响限流归属。

1. 浏览器登录 [gemini.google.com](https://gemini.google.com)
2. DevTools (F12) → Application → Cookies → `https://gemini.google.com`
3. 复制：`SID` / `HSID` / `SSID` / `APISID` / `SAPISID` / `__Secure-1PSID`
4. `cookie.txt`：
```
SID=...; HSID=...; SSID=...; APISID=...; SAPISID=...; __Secure-1PSID=...
```
5. 启动加 `--cookie-file cookie.txt`

## 代理池（白嫖路线核心）

**为什么需要**：实测单 IP 累积约 **85 次成功请求**后就会被重定向到
`google.com/sorry/index`，后续请求全部失败。

压测数据（5 个独立出口 IP，每个打到被拦为止）：

| IP | 首次被拦于第几次请求 | 此前成功次数 |
|---|---|---|
| 1 | 168 | ~92 |
| 2 | 87 | ~86 |
| 3 | 85 | ~84 |
| 4 | 109 | ~84 |
| 5 | 106 | ~83 |

**Google 数的是成功请求，不是尝试次数**——IP1 打了 168 次才被拦，因为其中 75 次
是代理连接失败、根本没到达 Google。请求次数分散在 85~168，但成功次数收敛在
83~92，后者才是稳定判据。

另一组对照：10 个 IP 各打 50 次（共 418 次请求），**零次 Google 拒绝**，失败全是
代理连接层的。所以 50 次以内完全安全。

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
| `POST /v1/chat/completions` | ✅ | 真流式：上游每出一帧就转发增量（实测 400 字中文回答产生 40 个 chunk）。chunk 序列为 `delta{role}` → `delta{content}`×N → 空 `delta`+`finish_reason` → `[DONE]`。**带 `tools` 时退化为收完再发**——tool_call 块要完整文本才能解析 |
| `POST /v1/responses` | ✅ | OpenAI Responses API（Codex CLI 用）。**未做真流式**，仍是收完再按事件序列发 |
| `GET /v1/models` | ✅ | 3 个模型 |
| Function calling | ⚠️ | Prompt 级实现（让模型输出 ` ```tool_call``` ` 块再 regex 解析），不是真协议层。**查私有数据/内部系统类可靠**，但 Gemini 自己能做的（如查天气）会被它直接回答，有副作用的动作（如发邮件）会被拒绝 |
| `tool_choice` | ⚠️ | `none` 完全不注入工具定义；`required` 和指定函数会加强制措辞、并把其余工具从 prompt 裁掉。但 prompt 级实现**无法真正强制**——实测模型自己答得上来的问题（天气、2+2）即使 `required` 也照样直接作答 |
| `stream_options.include_usage` | ✅ | 在 `finish_reason` 之后补一个 `choices` 为空的 usage chunk |
| `usage` token 数 | ✅ | tiktoken cl100k_base，与管理面板 requests 表同口径 |
| `n` > 1 | ❌ | 返回 400。上游只给一个候选，静默按 1 处理会让客户端少拿结果 |
| 采样参数 | ➖ | `temperature` / `top_p` / `max_tokens` / `stop` / `seed` / `presence_penalty` / `frequency_penalty` **收下即忽略，不报错**。Gemini 网页协议没有这些旋钮 |
| `response_format` / `logprobs` | ➖ | 未实现，收下即忽略 |
| Vision / 图片输入 | ❌ | 传 `image_url` 返回 400。匿名**能**上传（`content-push.googleapis.com/upload/` 两步 resumable，返回 `/contrib_service/ttl_1d/…`），但对话引用该文件被上游拒绝（`BardErrorInfo 1100`），需要登录态 |
| Audio | ❌ | 传 `input_audio` 返回 400。网页端有音乐生成（Lyria 3），需要登录态 |

## 项目结构

```
main.go              入口 + flag 解析 + 路由注册
config.go            配置加载 + DEFAULT_CONFIG
client.go            tls-client (chrome146) + stdlib (代理) 双 client
gemini.go            模型表 + 80 槽位 payload + 模型 header + StreamGenerate + wrb.fr 流解析
messages.go          OpenAI messages → prompt + tool_call 解析
server.go            /v1/* + 限流入口 + 请求字段校验 + metrics 写入
sse.go               chat.completion.chunk 的 SSE 写出器（懒发 header）
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

- **匿名访问**：单 IP 约 **85 次成功请求**后被重定向到 sorry 页（实测 83-92，n=5）→ 配代理池放大产能。默认 `per_ip_rph=80` 就是照这个留的余量
- **Pro 路由**：拿不到。免费账号即使挂 cookie 登录，`gemini-3.1-pro` 也会被降级到 3.6 Flash；付费订阅未验证
- **Function calling**：prompt 级实现，模型不一定每次都按格式返回（OpenAI 真协议层我们做不到）
- **多模态**：暂不支持。网页协议支持图片/文件上传、生图、音乐、视频，但都要登录态，本项目尚未实现
- **token 数**：用 tiktoken 估算（Gemini 真 tokenizer 未公开），跟真值偏差 ±20% 以内

## 致谢

- 社区 Python 单文件参考实现（同名 `gemini-web2api`）—— 协议层（80 槽位 payload、wrb.fr 解析、模型 ID 映射）的参考来源
- [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client) — Chrome 真指纹 TLS 库
- [pkoukk/tiktoken-go](https://github.com/pkoukk/tiktoken-go) — BPE tokenizer
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — 纯 Go SQLite（CGO-free，alpine 直接编）

## License

MIT — 详见 [LICENSE](LICENSE)
