"""生成 README 横幅（docs/banner.svg）。

用法：python docs/banner_gen.py
改了 streamGenerate 里的 inner 槽位就重跑一次，图会跟着更新。


图形本体是本项目最独特的东西：Gemini 网页协议的 inner 参数数组——80 个格子的
稀疏定位数组（下标即 protobuf 字段号），绝大多数是 null，只有少数真正携带请求。
点亮的格子是从 gemini.go 里抄来的真实下标，不是随便画的。

配色沿用管理面板已有的品牌渐变 #4285F4 → #9B72CB → #D96570，
在面板里它只出现在一处（封禁红线），这里同样只用在一处。
"""
import io
import os
import re

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# ── 从源码里读真实下标，避免图和代码脱节 ──────────────────────────────────
SRC = os.path.join(REPO, "gemini.go")
src = io.open(SRC, encoding="utf-8").read()
body = src[src.index("func streamGenerate("):src.index("innerJSON, err := json.Marshal(inner)")]
USED = sorted({int(m) for m in re.findall(r"inner\[(\d+)\]\s*=", body)})
TOTAL = 80
print(f"从 gemini.go 读到 {len(USED)} 个已用槽位: {USED}")

# ── 品牌渐变取色 ────────────────────────────────────────────────────────
STOPS = [(0.00, (0x42, 0x85, 0xF4)),
         (0.58, (0x9B, 0x72, 0xCB)),
         (1.00, (0xD9, 0x65, 0x70))]


def sweep(t):
    t = max(0.0, min(1.0, t))
    for i in range(len(STOPS) - 1):
        p0, c0 = STOPS[i]
        p1, c1 = STOPS[i + 1]
        if p0 <= t <= p1:
            k = 0 if p1 == p0 else (t - p0) / (p1 - p0)
            return tuple(round(c0[j] + (c1[j] - c0[j]) * k) for j in range(3))
    return STOPS[-1][1]


def hexc(rgb):
    return "#%02X%02X%02X" % rgb


# ── 版面 ────────────────────────────────────────────────────────────────
W, H = 1280, 276
CELL_W, GAP = 12.0, 3.4
ROW_W = TOTAL * CELL_W + (TOTAL - 1) * GAP
X0 = (W - ROW_W) / 2
ROW_Y, ROW_H = 142.0, 44.0

MONO = "ui-monospace,SFMono-Regular,SF Mono,Menlo,Consolas,Liberation Mono,monospace"

parts = []
parts.append(
    f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
    f'viewBox="0 0 {W} {H}" role="img" '
    f'aria-label="gemini-web2api-go — Gemini web protocol to OpenAI-compatible API">')

parts.append("""<defs>
  <linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">
    <stop offset="0" stop-color="#0C1220"/><stop offset="1" stop-color="#070B14"/>
  </linearGradient>
  <linearGradient id="sweep" x1="0" y1="0" x2="1" y2="0">
    <stop offset="0" stop-color="#4285F4"/><stop offset="0.58" stop-color="#9B72CB"/>
    <stop offset="1" stop-color="#D96570"/>
  </linearGradient>
  <filter id="glow" x="-60%" y="-60%" width="220%" height="220%">
    <feGaussianBlur stdDeviation="2.4" result="b"/>
    <feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge>
  </filter>
</defs>""")

parts.append(f'<rect width="{W}" height="{H}" fill="url(#bg)"/>')
# 顶部一条极细的渐变线，全图唯一的满宽亮色
parts.append(f'<rect x="0" y="0" width="{W}" height="2" fill="url(#sweep)" opacity=".85"/>')

# ── 标题 ────────────────────────────────────────────────────────────────
parts.append(
    f'<text x="{X0}" y="72" font-family="{MONO}" font-size="34" font-weight="600" '
    f'letter-spacing="-0.5" fill="#F2F5FA">gemini-web2api-go</text>')
parts.append(
    f'<text x="{X0}" y="102" font-family="{MONO}" font-size="14.5" '
    f'fill="#7C8AA5" letter-spacing="0.2">'
    f'gemini.google.com  <tspan fill="#9B72CB">&#8594;</tspan>  '
    f'OpenAI-compatible /v1  <tspan fill="#2D3B55">|</tspan>  single Go binary'
    f'</text>')

# 右上角：对外暴露的模型。跟标题分居两侧，把构图的重心拉平。
MODELS = [("gemini-3.6-flash", "#8FA0BE"),
          ("gemini-3.5-flash-lite", "#66748F"),
          ("gemini-3.1-pro", "#66748F")]
for k, (name, col) in enumerate(MODELS):
    parts.append(
        f'<text x="{X0 + ROW_W:.0f}" y="{58 + k * 19}" font-family="{MONO}" '
        f'font-size="12.5" fill="{col}" text-anchor="end">{name}</text>')

# ── 主体：80 格参数数组 ─────────────────────────────────────────────────
used = set(USED)
for i in range(TOTAL):
    x = X0 + i * (CELL_W + GAP)
    if i in used:
        c = hexc(sweep(i / (TOTAL - 1)))
        parts.append(
            f'<rect x="{x:.1f}" y="{ROW_Y:.0f}" width="{CELL_W:.0f}" height="{ROW_H:.0f}" '
            f'rx="2.5" fill="{c}" filter="url(#glow)"/>')
    else:
        # 空槽：只留一段细底线，暗示"位置还在，只是没传值"
        parts.append(
            f'<rect x="{x:.1f}" y="{ROW_Y + ROW_H - 3:.0f}" width="{CELL_W:.0f}" height="3" '
            f'rx="1.5" fill="#202B42"/>')

# 已用槽位的下标刻度（只标首尾和几个关键位，避免糊成一片）
LABEL_AT = [0, 2, 17, 41, 59, 79]
for i in LABEL_AT:
    x = X0 + i * (CELL_W + GAP) + CELL_W / 2
    parts.append(
        f'<text x="{x:.1f}" y="{ROW_Y + ROW_H + 20:.0f}" font-family="{MONO}" '
        f'font-size="11" fill="#55648A" text-anchor="middle">{i}</text>')

# ── 底部说明：告诉读者刚才看的是什么 ────────────────────────────────────
parts.append(
    f'<text x="{X0}" y="{H - 26}" font-family="{MONO}" font-size="13" fill="#5E6E8C">'
    f'<tspan fill="#8FA0BE">f.req</tspan> inner array &#183; '
    f'{TOTAL} positional slots, {len(USED)} carry the request</text>')

parts.append("</svg>")

OUT = os.path.join(REPO, "docs", "banner.svg")
os.makedirs(os.path.dirname(OUT), exist_ok=True)
io.open(OUT, "w", encoding="utf-8").write("\n".join(parts))
print(f"已写 {OUT}  ({os.path.getsize(OUT)} 字节)")
