package main

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
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function ToolCallFunction   `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// messagesToPrompt converts OpenAI messages[] (+ optional tools[]) to a single
// prompt string for Gemini. Tool schemas are embedded as a system instruction
// telling the model to emit ```tool_call``` blocks.
func messagesToPrompt(messages []map[string]interface{}, tools []map[string]interface{}) string {
	var parts []string

	if len(tools) > 0 {
		var defs []map[string]interface{}
		for _, tool := range tools {
			fn := tool
			if t, ok := tool["type"].(string); ok && t == "function" {
				if f, ok := tool["function"].(map[string]interface{}); ok {
					fn = f
				}
			}
			defs = append(defs, map[string]interface{}{
				"name":        getStr(fn, "name"),
				"description": getStr(fn, "description"),
				"parameters":  fn["parameters"],
			})
		}
		if len(defs) > 0 {
			defsJSON, _ := json.MarshalIndent(defs, "", "  ")
			parts = append(parts, fmt.Sprintf(
				"[System instruction]: You have access to tools. "+
					"To call a tool, respond with:\n"+
					"```tool_call\n{\"name\": \"func_name\", \"arguments\": {...}}\n```\n"+
					"Only use tool_call blocks when needed.\n\n"+
					"Available tools:\n%s", string(defsJSON),
			))
		}
	}

	for _, msg := range messages {
		role := getStr(msg, "role")
		content := contentToString(msg["content"])

		switch role {
		case "system":
			parts = append(parts, "[System instruction]: "+content)
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
				parts = append(parts, "[Assistant]: "+content+"\n"+strings.Join(tcStrs, "\n"))
			} else {
				parts = append(parts, "[Assistant]: "+content)
			}
		case "tool":
			parts = append(parts, fmt.Sprintf("[Tool result for %s]: %s", getStr(msg, "name"), content))
		default:
			if content != "" {
				parts = append(parts, content)
			}
		}
	}

	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
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
