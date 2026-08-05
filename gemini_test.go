package main

import "testing"

// 确认每个模型名解析出的 hex id 和 header 值正确。
func TestModelHeader(t *testing.T) {
	cases := []struct{ name, wantHex string }{
		{"gemini-3.6-flash", hexFlash36},
		{"gemini-3.5-flash", hexFlash36},
		{"gemini-auto", hexFlash36},
		{"gemini-3.5-flash-thinking", hexFlash36},
		{"gemini-3.5-flash-lite", hexFlashLite},
		{"gemini-flash-lite", hexFlashLite},
		{"gemini-3.1-pro", hexPro31},
	}
	for _, c := range cases {
		name, mc, err := resolveModel(c.name)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if mc.HexID != c.wantHex {
			t.Errorf("%s: hex=%s want %s", c.name, mc.HexID, c.wantHex)
		}
		h := buildGeminiHeaders("", "", mc.HexID)
		got := h["x-goog-ext-525001261-jspb"]
		want := `[1,null,null,null,"` + c.wantHex + `"]`
		if got != want {
			t.Errorf("%s: header=%s want %s", c.name, got, want)
		}
		t.Logf("%-32s -> mode=%d header=%s", name, mc.Mode, got)
	}

	// @think=N 是历史遗留的假参数，剥掉后忽略，不报错也不改变路由
	name, mc, err := resolveModel("gemini-3.6-flash@think=2")
	if err != nil || name != "gemini-3.6-flash" || mc.HexID != hexFlash36 {
		t.Errorf("@think 后缀应被忽略, got name=%s hex=%s err=%v", name, mc.HexID, err)
	}
	// 未知模型仍然报错，不静默回落
	if _, _, err := resolveModel("no-such-model"); err == nil {
		t.Error("unknown model should error")
	}
}
// 抓包里真实的"被拒绝"响应：只有结束帧，没有内容帧（216 字节）。
// 注意结束帧里带 BardErrorInfo[1096]，但正常响应的结束帧同样带这个码，
// 所以判据只能是"有没有内容帧"。
const rejectedRaw = ")]}'\n\n122\n" +
	`[["wrb.fr",null,null,null,null,[13,null,[["type.googleapis.com/assistant.boq.bard.application.BardErrorInfo",[1096]]]]]]` +
	"\n56\n" + `[["di",192],["af.httprm",191,"8196459853603899163",2]]` +
	"\n25\n" + `[["e",4,null,null,216]]` + "\n"

// 正常响应：有内容帧，且结束帧同样带 1096。
const okRaw = ")]}'\n\n900\n" +
	`[["wrb.fr",null,"[null,[\"c_x\",\"r_y\"],null,null,[[\"rc_z\",[\"banana\"],null,null,null,null,null,null,[2],\"en\"]],null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,\"3.6 Flash\"]"]]` +
	"\n122\n" +
	`[["wrb.fr",null,null,null,null,[13,null,[["type.googleapis.com/assistant.boq.bard.application.BardErrorInfo",[1096]]]]]]` + "\n"

func TestEmptyFrameDetection(t *testing.T) {
	if got := extractResponseText(rejectedRaw); got != "" {
		t.Errorf("被拒响应应解析出空文本, got %q", got)
	}
	if got := extractResponseText(okRaw); got != "banana" {
		t.Errorf("正常响应应解析出 banana, got %q", got)
	}
}

// usage 必须跟 requests 表口径一致（都走 tiktoken），不能退回 chars/4。
func TestUsageUsesTokenizer(t *testing.T) {
	initTokenizer()
	// 取一段 tiktoken 与 chars/4 结果明显不同的样本，否则断言区分不出新旧实现。
	prompt := "请详细解释注意力机制的计算过程，并说明多头注意力为什么有效。"
	text := "自注意力通过查询、键、值三组投影计算token之间的相关性权重。"

	if tokenizerOK && countTokens(prompt) == len(prompt)/4 {
		t.Skipf("样本无区分度（tiktoken 与 chars/4 同为 %d），换样本再测", countTokens(prompt))
	}

	u := buildUsage(prompt, text, false)
	if u["prompt_tokens"] != countTokens(prompt) || u["completion_tokens"] != countTokens(text) {
		t.Errorf("chat usage 没走 countTokens: %v", u)
	}
	if u["total_tokens"] != u["prompt_tokens"]+u["completion_tokens"] {
		t.Errorf("total 不等于两者之和: %v", u)
	}
	if tokenizerOK && u["prompt_tokens"] == len(prompt)/4 {
		t.Errorf("prompt_tokens 落在 chars/4 上，仍是旧实现: %v", u)
	}

	r := buildUsage(prompt, text, true)
	if r["input_tokens"] != countTokens(prompt) || r["output_tokens"] != countTokens(text) {
		t.Errorf("responses usage 没走 countTokens: %v", r)
	}
	if _, ok := r["prompt_tokens"]; ok {
		t.Error("responses API 不应出现 prompt_tokens 字段")
	}
}
