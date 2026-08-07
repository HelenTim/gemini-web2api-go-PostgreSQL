# gemini-web2api-go

<img src="docs/banner.svg" alt="gemini-web2api-go" width="100%">

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8.svg)](https://golang.org)
[![Docker](https://img.shields.io/badge/docker-distroless-blue)](Dockerfile)

中文 | [English](README_EN.md)

把 Google Gemini 网页端反代成 OpenAI 兼容 API。**单二进制**，**零账号**（匿名可跑），**Chrome 146 真指纹**，**SQLite 持久化**，自带**中文管理面板**。

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

## 功能

**接口**
- OpenAI 兼容：`/v1/chat/completions`、`/v1/models`、`/v1/responses`
- 普通对话真流式：上游每出一帧就转发增量（带 `tools` 的请求和 `/v1/responses` 是收完再发）
- Bearer token / `x-api-key` 鉴权，key 可在面板轮换
- `usage` 用 tiktoken 算，`reasoning_tokens` 单列不计入 `completion_tokens`

**模型**
- `gemini-3.6-flash`、`gemini-3.5-flash-lite` 匿名可用，含联网搜索
- `gemini-3.1-pro` 挂 cookie 后可用，每次回答带思考链（`reasoning_content`）
- 响应里记录服务端**实际**用了哪个模型，被静默降级一眼可见

**不被拦 / 跑得久**
- utls 模拟 Chrome 146 真 TLS 指纹，不是 SDK 默认握手
- 每个出口 IP 独立限流：并发 / RPM / RPH 三档
- 代理池：运行时增删改、失败熔断、轮转调度，每个代理是独立限流槽
- Cookie 池：多个 Google 账号按最久未用优先轮转

**运维**
- 单二进制，交叉编译 6 平台；容器镜像基于 distroless
- SQLite 持久化：30 天请求明细 + 永久聚合统计
- 中文管理面板：概览 / 请求记录 / 代理池 / Cookie 池 / 设置，配置改完即时生效
- **prompt 和回复内容永不入库**，只存元数据（长度、耗时、模型、状态）

## 快速开始

### 下载二进制（最省事）

[Releases](https://github.com/zexadev/gemini-web2api-go/releases) 里挑对应平台的下载，
不用 Go、不用 Docker，单个文件就是全部：

```bash
chmod +x gemini-web2api-go_*
./gemini-web2api-go_* --port 8083 --admin-token your-admin-token
```

数据默认落在 `./data/gemini.db`，换位置加 `--db /your/path.db`。

### Docker（不用源码）

```bash
docker run -d --name gemini-web2api \
  -p 127.0.0.1:8083:8083 \
  -v "$PWD/data:/data" \
  -e ADMIN_TOKEN=your-admin-token \
  ghcr.io/zexadev/gemini-web2api-go:latest
```

用 compose 的话把 `docker-compose.yml` 单独下下来就行，也不用 clone：

```bash
curl -O https://raw.githubusercontent.com/zexadev/gemini-web2api-go/main/docker-compose.yml
ADMIN_TOKEN=your-admin-token docker compose up -d
```

### 从源码跑

装了 Go 就不必绕 Docker：

```bash
git clone https://github.com/zexadev/gemini-web2api-go
cd gemini-web2api-go
go build -o gemini-web2api-go .
./gemini-web2api-go --port 8083 --admin-token your-admin-token
```

改了代码想用容器跑，把 `docker-compose.yml` 里 `build:` 那两行的注释去掉，再 `docker compose up -d --build`。

启动后会看到 banner：

```
gemini-web2api-go v4.0.0
  Listening:   http://0.0.0.0:8083
  Base URL:    http://localhost:8083/v1
  API key:     sk-gemini-XX...XXXX  (mutable in admin UI)
  Admin UI:    http://localhost:8083/admin  (token auth)
  DB:          ./data/gemini.db
  Models:      [gemini-3.5-flash-lite gemini-3.6-flash]
  Cookie:      none (anonymous)
  Proxy:       none
  Impersonate: chrome_146
  Tokenizer:   tiktoken cl100k_base
  Per-IP 限流: 并发=5 / RPM=30 / RPH=80
  Retry:       3x / 2s
```

首次启动会自动生成 API key（banner 里是打码的，完整值在管理面板「设置」页）。

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

Windows PowerShell 下 `curl` 是 `Invoke-WebRequest` 的别名，会把 JSON 引号重新解释，要用
`curl.exe` 加 `--%`：

```powershell
curl.exe --% http://127.0.0.1:8083/v1/chat/completions -H "Content-Type: application/json" -H "Authorization: Bearer sk-gemini-..." -d "{\"model\":\"gemini-3.6-flash\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello!\"}]}"
```

## 客户端接入

任何 OpenAI 兼容客户端（Cherry Studio / ChatBox / Open WebUI / dify / Cursor / …）都是同一套填法：

| 字段 | 值 |
|---|---|
| Base URL / API 地址 | `http://localhost:8083/v1`（部分客户端只要 `http://localhost:8083`，会自己拼 `/v1`） |
| API Key | 管理面板「设置」页里的那个 `sk-gemini-…` |
| 模型 | `gemini-3.6-flash` |

**newapi / one-api 建渠道**：类型选 OpenAI，Base URL 填 `http://localhost:8083`（跟 newapi 同在
docker 里就写 `http://host.docker.internal:8083` 或容器名），密钥填 API key，模型填
`gemini-3.6-flash,gemini-3.5-flash-lite`。

**Codex CLI** 走 `/v1/responses`，把 base url 指到 `http://localhost:8083/v1` 即可（该端点已实现，
但不是增量流式，见下面的协议覆盖表）。

**Gemini CLI 接不了**：它要的是 Google 原生的 `/v1beta/models/{model}:generateContent`，本项目只暴露
OpenAI 形状的接口，没做 `/v1beta`。

另有一个不鉴权的健康检查 `GET /`，返回 `{"status":"ok","version":…,"models":[…]}`，给探活用。

## 管理面板

`http://localhost:8083/admin`，用 `--admin-token` 登录。

- **概览** — 24h KPI + 请求量/P50 延迟双轴趋势图 + 模型/代理分组统计 + IP 限流用量 + 一键连通性诊断
- **请求记录** — 明细列表（仅元数据，无 prompt/response 内容），状态/模型筛选 + 分页
- **代理池** — 运行时增删改 + 启用/禁用 + 失败次数熔断（每代理是独立 IP slot）
- **Cookie 池** — 导入多个 Google 登录态账号，请求按**最久未用优先**自动轮转，池空才回落「设置」页那一个 cookie。列表只显示脱敏摘要（cookie 数 / 关键项 / SAPISID 末 4 位 / 失败次数）
- **设置** — 运行时配置表单（保存即生效）+ API Key 轮换 + Cookie 粘贴 + 部署期配置只读展示

面板前端是单个 HTML，Chart.js 随二进制 embed，**不走 CDN**——内网/离线部署也能开。

## 模型

Gemini 网页端服务端只认三个模型（清单来自 `batchexecute?rpcids=otAQ7b`）：

| 模型 | 描述 |
|---|---|
| `gemini-3.6-flash` | 全方位，默认 |
| `gemini-3.5-flash-lite` | 极速、轻量 |
| `gemini-3.1-pro` | 最强，**要配 cookie**；每次回答都带思考链 |

没配 cookie 时 `/v1/models` 只返回前两个，选 `gemini-3.1-pro` 会直接报错并说明
原因。因为匿名请求它必然被静默降级成 3.5 Flash-Lite——与其让客户端拿到一个
"成功但其实不是 Pro"的回复，不如在选型时就失败。

配了有效 cookie 时它是**真的 Pro**：连打 6 次服务端回报的都是 `3.1 Pro` 本身。

只暴露这三个。旧的 `gemini-3.5-flash`、`gemini-3.5-flash-thinking`、
`gemini-3.5-flash-thinking-lite`、`gemini-auto`、`gemini-flash-lite` **已移除**
（传了会返回 400）——它们在服务端没有对应条目，留着只会让人以为有五种不同
的模型可选。

> **`@think=N` 已废弃。** 该后缀写进请求的 `inner[17]`，一直被当作"思考深度"，
> 但抓包证明它是**会话内的轮次索引**（首轮 `[[0]]`，带会话 id 的第二轮 `[[1]]`，
> 逐轮递增），跟思考深度无关。我们每次都开新会话，该值恒为 0，所以这个参数
> 从来没有生效过。后缀仍被接受但直接忽略，不影响路由。

### 思考链（reasoning_content）

`gemini-3.1-pro` 每次回答前会先输出一段自己的推理过程，本项目把它按事实标准的
`reasoning_content` 字段暴露出来，newapi、Cherry Studio、ChatBox 等客户端会渲染成
可折叠的「思考过程」。另外两个模型不产出思考链，此时不会出现这个字段。

非流式：

```json
{"choices":[{"message":{
  "role":"assistant",
  "content":"The core rule of this classic puzzle is...",
  "reasoning_content":"**Defining the Constraints**

I've successfully defined..."
}}],
 "usage":{"prompt_tokens":26,"completion_tokens":337,"reasoning_tokens":77,"total_tokens":363}}
```

流式按 `delta.reasoning_content` 推，且**思考块全部推完才开始推正文**（上游就是这个
顺序），客户端观感跟网页端一致。

`reasoning_tokens` **单独计数、不含在 `completion_tokens` 里**：思考链默认被客户端
折叠不展示，算进去等于让用户为看不见的输出买单（下游 newapi 按 `completion_tokens`
计费）。要计费的自己把两者相加。

### 已知的能力边界

匿名调用（不挂 cookie）只能拿到上面两个文本模型 + Gemini 自带的联网搜索。
`gemini-3.1-pro` 匿名时被静默降级成 3.5 Flash-Lite，所以干脆不暴露。

生图、音乐、视频、深度研究、画布都需要登录，且**本项目尚未实现**——它们要在请求里
多带工具开关位，而我们的参数数组还没开那么长。管理面板的「实际模型」列会把服务端
实际用了哪个模型标出来，降级一眼可见。

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

`API_KEY` 环境变量会锁死 API key（面板改不了），用于不希望运行时被改的部署。

`--proxy` 和 `--cookie-file` 是**播种参数**，不是第二套配置：启动时把值导入代理池 /
Cookie 池（按 URL、cookie 内容去重），之后一律从面板管理。改了重启即生效。

命令行参数仍然可用，定位是本地调试时的临时覆盖。优先级：
**面板改动 > CLI flag / `config.json` > 内置默认**。

不用 Docker 直接跑二进制时，可以把 `config.example.json` 复制成 `config.json` 当启动模板
（不传 `--config` 时会自动找当前目录的 `config.json`，其次
`$HOME/.config/gemini-web2api/config.json`）。里面的调优项面板改了就以面板为准，
`config.json` 只决定第一次启动的初值。

命令行参数全集：

| flag | 说明 |
|---|---|
| `--port` | 监听端口，默认 8083 |
| `--config` | 指定 `config.json` 路径 |
| `--db` | SQLite 路径，默认 `./data/gemini.db` |
| `--admin-token` | 面板登录 token，留空 = 面板不鉴权（只有绑 127.0.0.1 才可接受） |
| `--api-key` | 锁定 `/v1/*` 的 key（面板改不了），等价于 `API_KEY` 环境变量 |
| `--cookie-file` | 启动时把文件里的 cookie 导入 Cookie 池 |
| `--proxy` | 启动时把这个代理导入代理池 |
| `--impersonate` | TLS 指纹档位 |
| `--version` | 打印版本退出（Docker healthcheck 用的就是它） |

## Cookie（可选）

挂 Google 账号 cookie 后请求走登录态，多出来的能力是 **`gemini-3.1-pro` + 思考链**
（见上文「思考链」一节）。免费账号实测可用，连打 6 次全部回报 `3.1 Pro`。

网页端登录后还能用生图（Nano Banana 2）、音乐（Lyria 3）、视频、深度研究、画布，
**本项目尚未实现这些**。

> 带 cookie 的请求必须额外携带一个 XSRF token，本项目会自动从 Gemini 页面取并按
> cookie 缓存、过期自动重取，无需配置。（这一步缺了会导致**所有**请求 400，
> 匿名反而不受影响——早期版本踩过这个坑。）

1. 浏览器登录 [gemini.google.com](https://gemini.google.com)
2. DevTools (F12) → Application → Cookies → `https://gemini.google.com`
3. 复制：`SID` / `HSID` / `SSID` / `APISID` / `SAPISID` / `__Secure-1PSID`
4. 粘进面板「Cookie 池 → 添加账号」；或写成 `cookie.txt` 后启动加
   `--cookie-file cookie.txt`（启动时导入池子，之后从面板管理）：
```
SID=...; HSID=...; SSID=...; APISID=...; SAPISID=...; __Secure-1PSID=...
```

JSON 形式 `{"cookie": "SID=...; ...", "sapisid": "..."}` 也吃。带 `SAPISID` 的请求会自动
算 `SAPISIDHASH` 授权头，所以这一项不能少。

**cookie 只有「Cookie 池」这一个入口**：每个请求按最久未用优先挑一个 enabled 账号，
挑中即推进轮转。池子里有 enabled 账号，`gemini-3.1-pro` 就会出现在模型列表里。

账号的「失败次数 / 最近成功」会随请求自动更新。判据是**只把 401/403 算作 cookie 的错**：
网络错误、代理失败、被 Google 拦（302）一律不计——住宅代理的出口退化率很高，把这些算
进去会让失败次数变成代理噪音，好 cookie 反而被记成失败最多的那个。

注意「最近成功」只说明这个 cookie 参与的请求成功过，**不等于 cookie 仍然有效**：cookie
过期后 Gemini 不报错，只是把你当匿名用户。想确认有效性就发一次 `gemini-3.1-pro`——有效
时回报 `3.1 Pro` 并带思考链，失效时降级成 3.5 Flash-Lite。

## 代理池（白嫖路线核心）

**为什么需要**：单 IP 突发地打到一定次数就会被重定向到 `google.com/sorry/index`。
这个次数是 **80-180**，跨度很大，由**连接策略、出口质量和请求节奏**共同决定：

| 出口 | 连接策略 | 并发 | 节奏 | 被拦时的成功次数 |
|---|---|---|---|---|
| 住宅 | 复用连接池 | 10 | 无间隔 | 151 / 172 / 177 |
| 住宅 | 复用连接池 | 3 | 无间隔 | 103 / 111 |
| 住宅 | 每次新建连接 | 10 | 无间隔 | 106 / 109 |
| 住宅 | 每次新建连接 | 1 | 间隔 24s | 81 / 166 |
| 静态 | 复用连接池 | 10 | 无间隔 | 188 |
| 静态 | 复用连接池 | 1 | **10 次/分钟** | **800 次没被拦** |

判据只认 302 → `/sorry/`，出口都预筛过。

**连接复用值约 60%**：并发钉死 10、两臂同时起跑、全程只跑 80 秒（短到出口来不及
漂移），复用连接 172/177，每次新建 106/109。

**平缓节奏比什么都管用**：同一个静态 IP，突发打在 188 次被拦，改成 10 次/分钟连打
**800 次、跨 110 分钟一次没被拦**。所以默认 `per_ip_rph=80` 是很保守的下沿，
明确按低速率跑的部署可以调高很多。

> 早前这里写「放慢节奏没用」，判据是住宅出口上突发档 103-177 与慢速档 81-166 几乎
> 完全重叠。那个观察没错，但归因错了：住宅出口跑久了自己会退化（8 个预筛干净的
> 出口跑慢节奏，6 个中途失败率超 40%），退化盖过了节奏的影响。换静态 IP 排除掉
> 这个混淆项之后，节奏的作用非常明显。

**代理失败会提前消耗额度**：链路越脏上限来得越早，因为那些"失败"的请求有一部分其实
已经到达 Google 并被计数（慢速档实际发出 195 次才换到 166 次成功，真实消耗高 17%）。
别指望靠重试失败请求多榨产能。

**被拦之后是硬拦，约两小时自动恢复。** 两次独立复测各探测 30 次、间隔 20s、
跨约 10 分钟，合计 60 次零成功；继续探测到 **106-121 分钟**之间恢复正常。
代理池的熔断冷却默认取 120 分钟就是照这个来的。

对照：10 个 IP 各打 50 次（418 请求）零次 Google 拒绝。默认 `per_ip_rph=80` 落在突发
档实测区间的下沿。

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

**不读 `HTTPS_PROXY` / `ALL_PROXY` 环境变量。** 代理只从代理池里取
（`--proxy` / `config.json` 只是启动时往池子里播种）——否则宿主机上一个随手 export 的变量会悄悄改变
出口 IP，而面板显示的还是直连，排查时会误判。

## 指纹模拟

直连场景（无代理）走 [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client)，TLS 握手 + HTTP/2 SETTINGS 帧 + ALPS 全部对齐真实 Chrome 146。Google 风控视角下，跟真浏览器无法区分。

走代理场景换用 stdlib `net/http` + `http.ProxyURL`（兼容性最佳），但应用层 header（`Sec-CH-UA` / `Sec-Fetch-*` / `User-Agent`）仍按 Chrome 146 真实值伪装。

**注意走代理时 TLS 指纹是我们自己的，不是出口节点的。** HTTP CONNECT 只建隧道，TLS
是我们跟 Google 端到端握的——JA3 回显实测：同一代理下 stdlib 和 tls-client 的 JA3
不同（代理没改写），而 stdlib 直连和过代理的 JA3 完全相同（代理层不介入 TLS）。所以
**配了代理，暴露给 Google 的就是 Go 标准库的指纹而不是 Chrome 146 的**。

但这不影响封禁阈值：同时起跑的对照里 tls-client Chrome_146 打到 111 次、Go stdlib
103 次，差 8 次，而同配置下出口之间的方差能到 36%（151 vs 111）。想让代理路径也走
Chrome 指纹得有别的理由（比如担心长期账号画像），不能指望换来产能。

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
| `GET /v1/models` | ✅ | 匿名 2 个，挂了 cookie 才出第 3 个 |
| `GET /` | ✅ | 健康检查，不鉴权，返回 status/version/models |
| `/v1beta/models/…`（Gemini CLI 原生格式） | ❌ | 未实现，只暴露 OpenAI 形状的接口 |
| `/v1/embeddings`、`/v1/images/*`、`/v1/audio/*` | ❌ | 未实现，返回 404 |
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
main.go                    只有一句 app.Run()；入口留在根目录，`go build .` 直接可用
internal/app/              全部实现
  app.go                   flag 解析 + 路由注册 + 启动
  config.go                配置加载 + 默认值
  client.go                tls-client (chrome146) + stdlib (走代理) 双 client
  gemini.go                模型表 + 80 槽 payload + 模型 header + StreamGenerate + wrb.fr 解析
  xsrf.go                  带 cookie 时必需的 XSRF token：抓取 + 按 cookie 缓存 + 过期自愈
  messages.go              OpenAI messages → prompt，tool_call 解析
  server.go                /v1/* + 限流入口 + 参数校验 + metrics 写入
  sse.go                   SSE 写出器（懒发 header，失败仍能返回 502 JSON）
  ratelimit.go             每 IP slot 独立并发 / RPM / RPH 限流
  tokenizer.go             tiktoken cl100k_base 单例
  apikey.go                API key（启动参数锁定 / 面板可轮换 双轨）
  db.go                    SQLite schema：sessions / requests / accounts / kv
  proxy.go                 代理池 CRUD + 容量调度 + 熔断
  cookie_pool.go           Cookie 池数据层（CRUD + 最久未用优先挑选 + 健康度回写）
  scheduler.go             小时/天聚合 + 过期明细清理
  runtime.go               运行时配置快照（面板改完即时生效）
  admin.go                 /admin/api/* 鉴权 + REST
  admin_cookies.go         Cookie 池的 admin REST（返回前脱敏）
  admin_ui.go              embed admin_ui/
  admin_ui/                管理面板前端（单页 + Chart.js，随二进制打包，不走 CDN）
  gemini_test.go           协议层单测：payload 槽位、wrb.fr 解析、模型门控、限流
Dockerfile                 多阶段构建（alpine builder → distroless runtime）
docker-compose.yml         单容器，默认拉 ghcr 镜像，sqlite 挂 volume
.github/workflows/         docker.yml 推镜像 / release.yml 打 tag 发二进制
```

## 限制

- **单 IP 上限**：突发地打，实测 **80-180 次请求**后被重定向到 sorry 页（连接复用能多打约 60%：并发 10 时复用 172/177、每次新建 106/109）。但**平缓打几乎打不满**——静态 IP 上 10 次/分钟连打 800 次没被拦。默认 `per_ip_rph=80` 取的是区间下沿 → 要放大产能配代理池，或按低速率跑并调高限额
- **登录态功能**：生图、音乐、视频、深度研究、画布都没实现（协议已验证可用，缺的是请求参数位）
- **Function calling**：prompt 级实现，模型不一定每次都按格式返回（OpenAI 真协议层我们做不到）
- **多模态**：暂不支持。网页协议支持图片/文件上传、生图、音乐、视频，但都要登录态，本项目尚未实现
- **token 数**：用 tiktoken 估算（Gemini 真 tokenizer 未公开），跟真值偏差 ±20% 以内
- **Cookie 池不自动摘除坏号**：请求成败会回写（只把 401/403 算作 cookie 的错，网络错误和 302 拦截不算），但失败到一定次数不会自动禁用，得看面板手动停。另外 `last_ok_at` 只说明"这个 cookie 参与的请求成功过"，不等于它仍然有效——cookie 过期后 Gemini 不报错，只是把你当匿名用户，纯文本请求照样 200
- **假流式的那一半**：`/v1/responses` 和带 `tools` 的 chat 请求都是收完再发，只有普通 chat 流式是真增量

## 故障排查

| 现象 | 多半是什么 | 怎么办 |
|---|---|---|
| 我们返回 **429** | 所有 IP slot 的并发/RPM/RPH 都占满了，**不是 Google 拒绝**，没消耗上游配额 | 加代理，或在「设置」页调高限额 |
| 面板诊断显示 **302 → `google.com/sorry/index`** | 这个出口 IP 被 Google 拦了（80-180 次请求后，取决于连接策略和出口质量） | 换出口/加代理。**是硬拦不是概率性**（被拦后 60 次探测零成功），原地重试没有意义 |
| 偶发空响应、面板记为上游拒绝 | 上游瞬时拒绝（`1155`），没有可预测阈值，跟频率/并发/累积次数都无关 | 重发一次通常就好。**降 RPM 解决不了**，实测跟频率无关 |
| 请求全部超时 | 本机到 `gemini.google.com` 不通 | 配代理（面板「代理池」或 `--proxy`）。注意**不读 `HTTPS_PROXY` 环境变量** |
| 选 `gemini-3.1-pro` 直接报错 | 没配 cookie 时它不暴露，这是故意的 | 挂 cookie（面板「设置」或「Cookie 池」）后即可用 |
| 挂了 cookie 后请求全部 502 | cookie 已失效，取不到 XSRF token | 重新导出 cookie。判据：请求 `gemini-3.1-pro` 若回报 3.5 Flash-Lite 就是失效了 |
| 面板打不开 / 401 | `--admin-token`（或 `ADMIN_TOKEN`）没对上 | token 留空则不鉴权，只有绑 127.0.0.1 时才可接受 |

Docker 用默认 bridge 网络时上游可能返回空内容（Google 拒绝某些 NAT 段）。本项目**没有复现过**，真遇到可以试 `network_mode: host` 验证是不是这个原因。

## 致谢

- [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client) — Chrome 真指纹 TLS 库
- [pkoukk/tiktoken-go](https://github.com/pkoukk/tiktoken-go) — BPE tokenizer
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — 纯 Go SQLite（CGO-free，alpine 直接编）

## License

MIT — 详见 [LICENSE](LICENSE)

## 友情链接

- [Sophomoresty/gemini-web2api](https://github.com/Sophomoresty/gemini-web2api) —— 同类的 Python 实现，早期摸 Gemini 网页协议时借鉴过它的思路
- [LINUX DO](https://linux.do) —— 本项目在该社区分享

[![LinuxDo](https://img.shields.io/badge/%E7%A4%BE%E5%8C%BA-LinuxDo-blue?style=for-the-badge)](https://linux.do/)

