package app

import (
	"fmt"
	"strings"
	"testing"
)

// 这一组的判据只有一条：**无论怎么超长，用户刚问的那句必须留在 prompt 里**。
//
// 上游单请求约 2 万 token 封顶，超了从**尾部**静默截断且不报错；而我们按顺序拼、
// 最新消息在末尾。两件事叠起来，被丢掉的正好是刚问的那句，模型只看到前面的系统
// 前言，于是回一句通用开场白，既不答题也不调工具。实测两个不同客户端的用户都栽
// 在这上面，表现都被误认成"模型变笨 / 工具支持不好"。

func longMsg(role string, n int) map[string]interface{} {
	return map[string]interface{}{"role": role, "content": strings.Repeat("历史填充内容。", n)}
}

func TestTrimKeepsNewestQuestion(t *testing.T) {
	const needle = "第七个行星叫什么"
	var msgs []map[string]interface{}
	msgs = append(msgs, map[string]interface{}{"role": "system", "content": "你是一个helpful assistant。"})
	for i := 0; i < 300; i++ { // 堆到远超预算
		msgs = append(msgs, longMsg("user", 20), longMsg("assistant", 20))
	}
	msgs = append(msgs, map[string]interface{}{"role": "user", "content": needle})

	parts := buildPromptParts(msgs, nil, nil)
	untrimmed, err := assemblePrompt(parts, 0) // 0 = 关掉保护，模拟旧行为
	if err != nil {
		t.Fatal(err)
	}
	if countTokens(untrimmed) <= 2000 {
		t.Fatalf("测试用例本身没超预算（%d token），构造有问题", countTokens(untrimmed))
	}

	got, err := assemblePrompt(parts, 2000)
	if err != nil {
		t.Fatalf("丢掉历史后应该能进预算: %v", err)
	}
	if !strings.Contains(got, needle) {
		t.Error("最新提问被丢掉了 —— 这正是要修的 bug")
	}
	if !strings.Contains(got, "helpful assistant") {
		t.Error("系统指令被丢掉了，模型会失去角色设定")
	}
	if !strings.Contains(got, droppedNotice) {
		t.Error("丢了历史却没告诉模型，它会以为对话本来就长这样")
	}
	if n := countTokens(got); n > 2000 {
		t.Errorf("丢完仍有 %d token，超预算 2000", n)
	}
}

// 工具定义必须活下来，否则模型不知道有哪些工具可调，tool_calls 直接没了。
func TestTrimKeepsToolDefs(t *testing.T) {
	tools := []map[string]interface{}{{"type": "function", "function": map[string]interface{}{
		"name": "read_file", "description": "读取一个文件",
		"parameters": map[string]interface{}{"type": "object"}}}}
	var msgs []map[string]interface{}
	for i := 0; i < 200; i++ {
		msgs = append(msgs, longMsg("user", 20), longMsg("assistant", 20))
	}
	msgs = append(msgs, map[string]interface{}{"role": "user", "content": "读取一下 task.md"})

	got, err := assemblePrompt(buildPromptParts(msgs, tools, nil), 2000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"read_file", "tool_call", "读取一下 task.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺了 %q", want)
		}
	}
}

// 不可丢的部分自己就超预算（系统提示或工具定义太大）时必须报错。
// 这时发出去只会让上游把最新提问截掉，客户端拿到一个答非所问的 200 却看不出问题。
func TestTrimErrorsWhenUndroppableTooBig(t *testing.T) {
	msgs := []map[string]interface{}{
		{"role": "system", "content": strings.Repeat("超长的系统提示词。", 5000)},
		{"role": "user", "content": "你好"},
	}
	_, err := assemblePrompt(buildPromptParts(msgs, nil, nil), 2000)
	if err == nil {
		t.Fatal("丢无可丢却没报错，等于把答非所问的结果推给用户")
	}
	var e *PromptTooLongError
	if !asPromptTooLong(err, &e) {
		t.Fatalf("错误类型不对: %T", err)
	}
	if !strings.Contains(err.Error(), "truncates") {
		t.Errorf("错误信息没解释上游会截断，用户不知道为什么: %v", err)
	}
}

// 没超预算时一个字都不能动。
func TestTrimNoopWhenUnderBudget(t *testing.T) {
	msgs := []map[string]interface{}{
		{"role": "system", "content": "系统提示"},
		{"role": "user", "content": "第一问"},
		{"role": "assistant", "content": "第一答"},
		{"role": "user", "content": "第二问"},
	}
	parts := buildPromptParts(msgs, nil, nil)
	full, _ := assemblePrompt(parts, 0)
	got, err := assemblePrompt(parts, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if got != full {
		t.Errorf("没超预算却改动了 prompt:\n%q\n%q", got, full)
	}
	if strings.Contains(got, droppedNotice) {
		t.Error("没丢东西却插了省略提示")
	}
}

// budget=0 表示关掉保护，退回旧行为（原样发出、由上游截断）。
func TestTrimDisabled(t *testing.T) {
	var msgs []map[string]interface{}
	for i := 0; i < 100; i++ {
		msgs = append(msgs, longMsg("user", 20))
	}
	got, err := assemblePrompt(buildPromptParts(msgs, nil, nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	if countTokens(got) < 2000 {
		t.Error("关掉保护后不该有任何裁剪")
	}
}

// 丢弃从**最旧**的开始：越靠后的历史越该留下。
func TestTrimDropsOldestFirst(t *testing.T) {
	var msgs []map[string]interface{}
	for i := 0; i < 60; i++ {
		msgs = append(msgs, map[string]interface{}{
			"role": "user", "content": fmt.Sprintf("消息%02d ", i) + strings.Repeat("填充。", 30)})
	}
	msgs = append(msgs, map[string]interface{}{"role": "user", "content": "最后一问"})

	got, err := assemblePrompt(buildPromptParts(msgs, nil, nil), 1500)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "消息00") {
		t.Error("最旧的没被先丢掉")
	}
	if !strings.Contains(got, "消息59") {
		t.Error("最近的历史被丢了，应该优先保留")
	}
	if !strings.Contains(got, "最后一问") {
		t.Error("最新提问被丢了")
	}
}

func asPromptTooLong(err error, target **PromptTooLongError) bool {
	e, ok := err.(*PromptTooLongError)
	if ok {
		*target = e
	}
	return ok
}
