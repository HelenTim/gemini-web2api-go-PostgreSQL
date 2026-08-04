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
		name, mc, think, err := resolveModel(c.name)
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
		t.Logf("%-32s -> mode=%d think=%d header=%s", name, mc.Mode, think, got)
	}

	// @think=N 覆盖仍然有效
	if _, _, th, _ := resolveModel("gemini-3.6-flash@think=2"); th != 2 {
		t.Errorf("@think=2 -> %d", th)
	}
	// 未知模型仍然报错，不静默回落
	if _, _, _, err := resolveModel("no-such-model"); err == nil {
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
