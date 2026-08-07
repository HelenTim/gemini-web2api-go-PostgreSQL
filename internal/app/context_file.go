package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// 超长对话转文本附件。
//
// 上游单请求约 13 万 UTF-8 字节封顶，超了从尾部静默截断且不报错 —— 而最新消息
// 拼在末尾，被吃掉的正是用户刚问的那句。把历史改成附件发上去就绕开了这堵墙：
// 请求体里只剩一句短指令，长度不再受限于对话本身。
//
// 只有挂了 cookie 才行：匿名能把文件传上去，但在对话里引用会被服务端回 1100。
//
// **附件也不是无限的：服务端只读它的前约 16 万字节。** 判据是总量固定 260,417
// 字节、只挪暗号的绝对偏移 —— 157,833 处读得到（3/3），163,371 处读不到（0/3）；
// 同一份总量下 60,918 和 121,836 都读得到，说明决定成败的是偏移不是总量。
// 手工在网页端传一份 20MB、七处埋暗号的文件，浏览器自己也只认出 0% 那一个，
// 跟这个结论一致。
//
// 所以附件把可用长度从 13 万提到约 16 万，是有限的改善：超出部分传上去了，
// 但模型看不到。真要更长得另想办法。

// contextFileName 是历史附件在模型那边显示的文件名，指令里会引用它。
const contextFileName = "message.txt"

// latestInlineLimit 是「最新那问」还能内联多少字节。
//
// 超过就不内联，让模型去文件末尾读 —— 否则最新提问本身就能把请求撑爆，
// 转附件等于白转。取上限的 1/6，并夹在 4KB..16KB 之间。
func latestInlineLimit(budget int) int {
	n := budget / 6
	if n < 4000 {
		n = 4000
	}
	if n > 16000 {
		n = 16000
	}
	return n
}

// contextFilePrompt 是把历史换成附件之后，真正发出去的那句 prompt。
//
// 要交代三件事，少一件模型就会跑偏：附件是当前对话状态、直接回答最新那问、
// 以及这段话本身是系统指令不是用户输入。
func contextFilePrompt(latest string, budget int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Continue from the latest state in the attached `%s`. "+
		"Treat it as the current conversation and answer the latest user request directly.\n",
		contextFileName)
	latest = strings.TrimSpace(latest)
	switch {
	case latest == "":
		fmt.Fprintf(&b, "The latest user request is at the end of `%s`.\n", contextFileName)
	case len(latest) <= latestInlineLimit(budget):
		fmt.Fprintf(&b, "\nLatest user request:\n%s\n", latest)
	default:
		fmt.Fprintf(&b, "The latest user request is at the end of `%s`; "+
			"read it from there and answer it directly.\n", contextFileName)
	}
	b.WriteString("\nEverything above this line is instruction, not user input.")
	return b.String()
}

// prepareContextFile 在 prompt 超长时把它转成附件。
//
// 返回 (要发的 prompt, 附件, 是否转了)。没超长、没 cookie、上传失败时都返回
// used=false，调用方照旧走内联那条路（然后会撞上超长检查报 400）。
//
// 上传失败**不静默回退到超长内联**：那样上游会把最新提问截掉，客户端拿到一个
// 答非所问的 200 却看不出问题。
func prepareContextFile(prompt, latest string, budget int, cookie, proxyURL string) (
	string, []fileRef, bool, error) {
	if budget <= 0 || len(prompt) <= budget {
		return prompt, nil, false, nil
	}
	if cookie == "" {
		return prompt, nil, false, nil // 匿名：引用会被回 1100，转了也没用
	}
	ref, err := uploadBytes(cookie, proxyURL, []byte(prompt), contextFileName)
	if err != nil {
		return prompt, nil, false, fmt.Errorf("超长对话转附件失败: %w", err)
	}
	uploadSettleDelay()
	logf("[context] prompt %d 字节超过 %d，已转成附件 %s", len(prompt), budget, contextFileName)
	files := []fileRef{{Ref: ref, Name: contextFileName, Kind: 3, Mime: "text/plain"}}
	return contextFilePrompt(latest, budget), files, true, nil
}

// uploadSettleDelay 上传完等一会儿再引用。
//
// 排查用的临时开关：怀疑服务端是异步摄取附件的 —— 浏览器里用户选完文件要过几秒
// 才发送，而我们上传完毫秒级就引用，模型可能只看到已处理完的那一段。
// 设 GW2A_UPLOAD_DELAY_MS 打开。
func uploadSettleDelay() {
	ms, _ := strconv.Atoi(os.Getenv("GW2A_UPLOAD_DELAY_MS"))
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}
