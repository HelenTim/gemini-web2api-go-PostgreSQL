package app

import (
	"strings"
	"testing"
)

// 判据：prompt 超过单请求上限时**明确报错**，绝不悄悄改动内容。
//
// 上游超限时从**尾部**静默截断且不报错，而最新消息拼在末尾，所以被吃掉的正好是
// 用户刚问的那句 —— 模型只看到前面的系统前言，回一句通用开场白，既不答题也不
// 调工具。实测两个不同客户端的用户都栽在这上面，都被误认成"模型变笨"。
//
// 我们自己丢历史同样不行：那还是静默丢数据，只是换了个地方丢。客户端以为整段都
// 发出去了，模型却忘了东西。报 context_length_exceeded 是 OpenAI 兼容客户端认得
// 的信号，agentic 客户端收到会自己压缩上下文再试。

func withBudget(t *testing.T, n int) {
	t.Helper()
	old := rtCfg()
	next := old
	next.MaxPromptTokens = n
	rtMu.Lock()
	rtVal = next
	rtMu.Unlock()
	t.Cleanup(func() {
		rtMu.Lock()
		rtVal = old
		rtMu.Unlock()
	})
}

func longConversation(turns int) []map[string]interface{} {
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是一个 helpful assistant。"},
	}
	for i := 0; i < turns; i++ {
		msgs = append(msgs,
			map[string]interface{}{"role": "user", "content": strings.Repeat("历史填充内容。", 20)},
			map[string]interface{}{"role": "assistant", "content": "Noted."})
	}
	return append(msgs, map[string]interface{}{"role": "user", "content": "读取一下 task.md"})
}

func TestPromptOverLimitErrors(t *testing.T) {
	withBudget(t, 2000)

	_, err := messagesToPrompt(longConversation(200), nil, nil)
	if err == nil {
		t.Fatal("超限却没报错——上游会把最新提问截掉，客户端拿到答非所问的 200 还看不出来")
	}
	e, ok := err.(*PromptTooLongError)
	if !ok {
		t.Fatalf("错误类型不对: %T", err)
	}
	if e.Budget != 2000 || e.Tokens <= e.Budget {
		t.Errorf("错误里的数字不对: %d tokens / budget %d", e.Tokens, e.Budget)
	}
	// 错误信息要说清为什么被拒，否则用户只会以为是我们坏了
	for _, want := range []string{"truncates", "latest message", "Shorten"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息缺少 %q: %v", want, err)
		}
	}
}

// 没超限时一个字都不能动——包括不能有任何"省略了部分历史"之类的注入。
func TestPromptUnderLimitUntouched(t *testing.T) {
	withBudget(t, 100000)
	msgs := longConversation(3)

	got, err := messagesToPrompt(msgs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := buildPrompt(msgs, nil, nil); got != want {
		t.Error("没超限却改动了 prompt")
	}
	if !strings.Contains(got, "读取一下 task.md") {
		t.Error("最新提问不在 prompt 里")
	}
}

// 0 = 关掉检查，退回旧行为（原样发出，由上游从尾部截断）。
func TestPromptLimitDisabled(t *testing.T) {
	withBudget(t, 0)
	msgs := longConversation(200)

	got, err := messagesToPrompt(msgs, nil, nil)
	if err != nil {
		t.Fatalf("关掉检查后不该报错: %v", err)
	}
	if countTokens(got) < 2000 {
		t.Error("关掉检查后不该有任何裁剪")
	}
}

// 上限判的是**整个 prompt**，工具定义也算进去——agentic 客户端的工具 schema
// 往往比对话本身还大，不算进去等于没设防。
func TestPromptLimitCountsToolDefs(t *testing.T) {
	withBudget(t, 500)
	tools := []map[string]interface{}{{"type": "function", "function": map[string]interface{}{
		"name": "read_file", "description": strings.Repeat("一个很长的工具描述。", 300),
		"parameters": map[string]interface{}{"type": "object"}}}}
	msgs := []map[string]interface{}{{"role": "user", "content": "你好"}}

	if _, err := messagesToPrompt(msgs, tools, nil); err == nil {
		t.Error("工具定义撑爆了预算却没报错")
	}
}
