package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// randHex returns n random hex chars. Equivalent to Python's
// `uuid.uuid4().hex[:n]` — produces a pure hex string of length n.
//
// Note: Go's `uuid.NewString()` returns the dashed 8-4-4-4-12 format,
// so naive slicing like `uuid.NewString()[:12]` would yield 11 hex + 1 dash.
// We sidestep that by drawing fresh random bytes.
func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// messagesToPrompt converts OpenAI messages[] (+ optional tools[]) to a single
// prompt string for Gemini. Tool schemas are embedded as a system instruction
// telling the model to emit ```tool_call``` blocks.
//
// toolChoice 是 OpenAI 的 tool_choice 字段（"none"/"auto"/"required" 或
// {"type":"function","function":{"name":...}}）。上游没有协议层的工具调用，
// 只能把约束写进指令。"required" 尤其必要：实测 Gemini 对自己能回答的问题
// （查天气之类）会直接作答而不调工具，不强制就拿不到 tool_call。
func messagesToPrompt(messages []map[string]interface{}, tools []map[string]interface{},
	toolChoice interface{}) (string, error) {
	return assemblePrompt(buildPromptParts(messages, tools, toolChoice), rtCfg().MaxPromptTokens)
}

// promptPart 是拼 prompt 时的一段，droppable 标记它在超长时能不能被丢掉。
//
// 不能丢的是系统指令（含工具定义）和**最新一条消息**；能丢的是中间的历史对话。
// 这个区分是 A1 的全部要点，见 assemblePrompt。
type promptPart struct {
	text      string
	droppable bool
}

func buildPromptParts(messages []map[string]interface{}, tools []map[string]interface{},
	toolChoice interface{}) []promptPart {
	var parts []promptPart
	// 系统指令 / 工具定义不可丢：丢了模型就不知道有哪些工具、该守什么规矩。
	keep := func(text string) { parts = append(parts, promptPart{text: text}) }
	// 历史对话可丢，超长时从最旧的开始。
	drop := func(text string) { parts = append(parts, promptPart{text: text, droppable: true}) }

	mode, forced := parseToolChoice(toolChoice)
	if len(tools) > 0 && mode != "none" {
		var defs []map[string]interface{}
		for _, tool := range tools {
			fn := tool
			if t, ok := tool["type"].(string); ok && t == "function" {
				if f, ok := tool["function"].(map[string]interface{}); ok {
					fn = f
				}
			}
			name := getStr(fn, "name")
			if forced != "" && name != forced {
				continue // 指定了函数名，其余不进 prompt
			}
			defs = append(defs, map[string]interface{}{
				"name":        name,
				"description": getStr(fn, "description"),
				"parameters":  fn["parameters"],
			})
		}
		if len(defs) > 0 {
			defsJSON, _ := json.MarshalIndent(defs, "", "  ")
			rule := "Only use tool_call blocks when needed."
			switch {
			case forced != "":
				rule = fmt.Sprintf(
					"You MUST call the tool %q. Reply with the tool_call block and nothing "+
						"else — do not answer the question yourself, even if you know the answer.",
					forced)
			case mode == "required":
				rule = "You MUST call one of the tools above. Reply with the tool_call block " +
					"and nothing else — do not answer the question yourself, even if you " +
					"know the answer."
			}
			keep(fmt.Sprintf(
				"[System instruction]: You have access to tools. "+
					"To call a tool, respond with:\n"+
					"```tool_call\n{\"name\": \"func_name\", \"arguments\": {...}}\n```\n"+
					"%s\n\n"+
					"Available tools:\n%s", rule, string(defsJSON),
			))
		}
	}

	for _, msg := range messages {
		role := getStr(msg, "role")
		content := contentToString(msg["content"])

		switch role {
		case "system":
			keep("[System instruction]: " + content)
		case "assistant":
			if tcs, ok := msg["tool_calls"].([]interface{}); ok && len(tcs) > 0 {
				var tcStrs []string
				for _, tc := range tcs {
					tcMap, ok := tc.(map[string]interface{})
					if !ok {
						continue
					}
					fn, _ := tcMap["function"].(map[string]interface{})
					name := getStr(fn, "name")
					args := getStr(fn, "arguments")
					if args == "" {
						args = "{}"
					}
					tcStrs = append(tcStrs, fmt.Sprintf(
						"```tool_call\n{\"name\": \"%s\", \"arguments\": %s}\n```",
						name, args,
					))
				}
				drop("[Assistant]: " + content + "\n" + strings.Join(tcStrs, "\n"))
			} else {
				drop("[Assistant]: " + content)
			}
		case "tool":
			drop(fmt.Sprintf("[Tool result for %s]: %s", getStr(msg, "name"), content))
		default:
			if content != "" {
				drop(content)
			}
		}
	}

	var out []promptPart
	for _, p := range parts {
		if p.text != "" {
			out = append(out, p)
		}
	}
	// 最新一条无论什么角色都不能丢——它就是用户此刻问的问题。
	// 上游超限时是从**尾部**截断，而我们把最新消息拼在末尾，两件事叠起来，
	// 被丢掉的正好是刚问的那句。这是这个保护要解决的根因。
	if n := len(out); n > 0 {
		out[n-1].droppable = false
	}
	return out
}

func contentToString(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var bits []string
		for _, c := range v {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			t := getStr(cm, "type")
			if t == "text" || t == "input_text" {
				bits = append(bits, getStr(cm, "text"))
			}
		}
		return strings.Join(bits, " ")
	default:
		return ""
	}
}

func getStr(m map[string]interface{}, k string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}

const (
	toolFenceOpen  = "```tool_call"
	toolFenceClose = "```"
)

// toolFenceGate 让带 tools 的请求也能真流式。
//
// 问题：上游没有协议层的工具调用，我们让模型吐 ```tool_call``` 围栏，而围栏要
// 完整文本才能解析。边出边转发会把围栏原文推给客户端 —— 客户端看到的是一段
// markdown 代码块，不是 tool_calls。所以这条路以前退化成收完再发。
//
// 解法：只发**确定不属于围栏**的部分。围栏内的全部扣住，最后由 parseToolCalls
// 统一转成 tool_calls。关键是尾巴上可能压着半个开围栏（比如只到两个反引号，
// 或者到 tool_c 就断了），那部分也得扣住等下一帧 —— 否则先发出去，下一帧才发现
// 它是围栏的开头，而已发出的内容收不回来。
//
// Sent() 是**实际发给客户端**的文本，跟 deltaTracker 的 emitted 不是一回事
// （后者含围栏原文）。收尾补发尾巴时必须拿这个比，否则前缀对不上，尾巴会丢。
type toolFenceGate struct {
	emit    func(string)
	buf     string // 还没判定完的尾巴
	sent    strings.Builder
	inFence bool
}

func newToolFenceGate(emit func(string)) *toolFenceGate {
	return &toolFenceGate{emit: emit}
}

// Sent 返回到目前为止实际发给客户端的全部文本。
func (g *toolFenceGate) Sent() string {
	if g == nil {
		return ""
	}
	return g.sent.String()
}

func (g *toolFenceGate) send(s string) {
	if s == "" {
		return
	}
	g.sent.WriteString(s)
	g.emit(s)
}

// Push 吃进一段增量文本，把确定不在围栏里的部分立刻发出去。
func (g *toolFenceGate) Push(delta string) {
	g.buf += delta
	for {
		if g.inFence {
			j := strings.Index(g.buf, toolFenceClose)
			if j < 0 {
				return // 围栏还没闭合，整段扣住
			}
			g.buf = g.buf[j+len(toolFenceClose):]
			g.inFence = false
			continue
		}
		if i := strings.Index(g.buf, toolFenceOpen); i >= 0 {
			g.send(g.buf[:i])
			g.buf = g.buf[i+len(toolFenceOpen):]
			g.inFence = true
			continue
		}
		keep := partialPrefixLen(g.buf, toolFenceOpen)
		g.send(g.buf[:len(g.buf)-keep])
		g.buf = g.buf[len(g.buf)-keep:]
		return
	}
}

// partialPrefixLen 返回 s 的末尾有多少字节是 marker 的前缀（不含完整匹配）。
//
// marker 全是 ASCII，所以匹配到的后缀必然也全是 ASCII，切点不会落在多字节
// 字符中间 —— UTF-8 的续字节 >=0x80，永远不等于 marker 里的任何字节。
func partialPrefixLen(s, marker string) int {
	max := len(marker) - 1
	if len(s) < max {
		max = len(s)
	}
	for k := max; k > 0; k-- {
		if strings.HasPrefix(marker, s[len(s)-k:]) {
			return k
		}
	}
	return 0
}

var toolCallRe = regexp.MustCompile("(?s)```tool_call\\s*\\n(.*?)\\n```")

// parseToolCalls extracts ```tool_call``` blocks. Returns clean text + tool_calls.
func parseToolCalls(text string) (string, []ToolCall) {
	var toolCalls []ToolCall
	for _, match := range toolCallRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		var data struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(match[1])), &data); err != nil {
			continue
		}
		argsJSON, _ := json.Marshal(data.Arguments)
		toolCalls = append(toolCalls, ToolCall{
			ID:   "call_" + randHex(8),
			Type: "function",
			Function: ToolCallFunction{
				Name:      data.Name,
				Arguments: string(argsJSON),
			},
		})
	}
	clean := toolCallRe.ReplaceAllString(text, "")
	return strings.TrimSpace(clean), toolCalls
}

// parseToolChoice 解析 OpenAI 的 tool_choice。
// 返回 (mode, forcedName)：mode ∈ {"auto","none","required"}；
// forcedName 非空表示客户端点名了某个函数。
func parseToolChoice(tc interface{}) (string, string) {
	switch v := tc.(type) {
	case string:
		switch v {
		case "none", "required", "auto":
			return v, ""
		}
	case map[string]interface{}:
		// {"type":"function","function":{"name":"..."}}
		if f, ok := v["function"].(map[string]interface{}); ok {
			if n := getStr(f, "name"); n != "" {
				return "required", n
			}
		}
	}
	return "auto", ""
}

// PromptTooLongError 表示丢完所有可丢的历史之后，prompt 仍然超过单请求上限。
type PromptTooLongError struct {
	Tokens, Budget int
}

func (e *PromptTooLongError) Error() string {
	return fmt.Sprintf(
		"prompt is %d tokens after dropping older history, over the %d-token per-request "+
			"limit of the Gemini web protocol. The upstream silently truncates from the end, "+
			"which would drop your latest message and produce an unrelated answer, so this "+
			"request is rejected instead. Shorten the system prompt or the tool definitions.",
		e.Tokens, e.Budget)
}

// 丢掉历史后插在原位，让模型知道中间断过，而不是以为对话本来就长这样。
const droppedNotice = "[Note]: some earlier turns were omitted because the conversation exceeded the single-request limit."

// assemblePrompt 把各段拼成最终 prompt，超长时从**最旧的历史**开始丢。
//
// 为什么必须我们自己丢：上游单请求约 2 万 token 封顶，超了**从尾部静默截断**且不
// 报错。而 messagesToPrompt 按顺序拼、最新消息在末尾 —— 于是被丢掉的正好是用户
// 刚问的那句。表现是模型答非所问、不调工具，看着像"变笨"或"工具支持不好"，
// 实测两个不同客户端的用户都栽在这上面。
//
// budget<=0 表示关掉保护，退回旧行为（原样发出去，由上游截断）。
func assemblePrompt(parts []promptPart, budget int) (string, error) {
	join := func(ps []promptPart) string {
		out := make([]string, 0, len(ps))
		for _, p := range ps {
			out = append(out, p.text)
		}
		return strings.Join(out, "\n\n")
	}
	if budget <= 0 || len(parts) == 0 {
		return join(parts), nil
	}

	full := join(parts)
	total := countTokens(full)
	if total <= budget {
		return full, nil
	}

	// 逐段计数，从最旧的可丢段开始摘，直到进预算内。
	// 分隔符和提示语也占 token，所以每摘一段都重算一次全文，别拿差值估。
	kept := append([]promptPart(nil), parts...)
	dropped := 0
	for {
		idx := -1
		for i := range kept {
			if kept[i].droppable {
				idx = i
				break
			}
		}
		if idx < 0 {
			break // 没有可丢的了
		}
		kept = append(kept[:idx], kept[idx+1:]...)
		dropped++
		// 提示语只插一次，插在被摘走的位置上
		if dropped == 1 {
			kept = append(kept[:idx], append([]promptPart{{text: droppedNotice}}, kept[idx:]...)...)
		}
		if countTokens(join(kept)) <= budget {
			logf("[prompt] 超出 %d token 上限，已丢弃 %d 段较早的对话", budget, dropped)
			return join(kept), nil
		}
	}

	// 不可丢的部分自己就超了：系统提示或工具定义太大。
	// 这时宁可明确报错，也不能发出去让上游把用户的问题截掉。
	return "", &PromptTooLongError{Tokens: countTokens(join(kept)), Budget: budget}
}
