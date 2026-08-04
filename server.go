package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const Version = "3.0.0"

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	body, _ := json.Marshal(data)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	w.Write(body)
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	var data []map[string]interface{}
	for name, c := range Models {
		data = append(data, map[string]interface{}{
			"id":          name,
			"object":      "model",
			"created":     1700000000,
			"owned_by":    "google",
			"description": c.Desc,
		})
	}
	writeJSON(w, 200, map[string]interface{}{"object": "list", "data": data})
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	var modelNames []string
	for n := range Models {
		modelNames = append(modelNames, n)
	}
	writeJSON(w, 200, map[string]interface{}{
		"status":  "ok",
		"version": Version,
		"models":  modelNames,
	})
}

func handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.WriteHeader(204)
}

func callGemini(prompt string, mc ModelConfig, thinkMode int, tools []map[string]interface{}) (string, []ToolCall, *StreamResult, error) {
	res, err := streamGenerate(prompt, mc, thinkMode)
	if err != nil {
		return "", nil, nil, err
	}
	text := extractResponseText(res.Raw)
	if text == "" {
		// 上游拒绝时只回一个结束帧、没有内容帧（实测多轮会话 id 被拒时 raw 仅 216
		// 字节）。这种情况必须报错：以前会当成空回复返回 200 + content:null，
		// 客户端看不出请求其实失败了。
		// 注意不能用 BardErrorInfo 判错 —— 正常响应的结束帧里也带这个码。
		return "", nil, res, fmt.Errorf("upstream returned no content frame (raw %d bytes)", len(res.Raw))
	}
	var toolCalls []ToolCall
	if len(tools) > 0 {
		text, toolCalls = parseToolCalls(text)
	}
	return text, toolCalls, res, nil
}

// recordRequest writes one row of metadata to the requests table.
// Privacy: the prompt/response strings themselves are never persisted —
// only their length, model name, latency, status, and proxy info.
func recordRequest(endpoint, model, prompt, response string, res *StreamResult, status int, errStr string, stream bool) {
	r := &RequestRow{
		TS:            time.Now().Unix(),
		Model:         model,
		Status:        status,
		Error:         errStr,
		PromptChars:   len(prompt),
		ResponseChars: len(response),
		PromptTokens:  countTokens(prompt),
		OutputTokens:  countTokens(response),
		Endpoint:      endpoint,
	}
	if stream {
		r.Stream = 1
	}
	if res != nil {
		r.TotalMs = res.TotalMs
		r.TTFBMs = &res.TTFBMs
		if res.ProxyID > 0 {
			r.ProxyID = &res.ProxyID
		}
		r.ProxyName = res.ProxyName
	}
	go insertRequest(r)
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		return
	}
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"error": map[string]string{"message": "invalid JSON"}})
		return
	}

	modelInput := getStr(req, "model")
	if modelInput == "" {
		modelInput = cfg.DefaultModel
	}
	modelName, modelCfg, thinkMode, err := resolveModel(modelInput)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		return
	}

	messagesRaw, _ := req["messages"].([]interface{})
	var messages []map[string]interface{}
	for _, m := range messagesRaw {
		if mm, ok := m.(map[string]interface{}); ok {
			messages = append(messages, mm)
		}
	}

	toolsRaw, _ := req["tools"].([]interface{})
	var tools []map[string]interface{}
	for _, t := range toolsRaw {
		if tm, ok := t.(map[string]interface{}); ok {
			tools = append(tools, tm)
		}
	}

	prompt := messagesToPrompt(messages, tools)
	if strings.TrimSpace(prompt) == "" {
		writeJSON(w, 400, map[string]interface{}{"error": map[string]string{"message": "empty prompt"}})
		return
	}

	text, toolCalls, res, err := callGemini(prompt, modelCfg, thinkMode, tools)
	if err != nil {
		recordRequest("chat.completions", modelName, prompt, "", nil, 502, err.Error(), false)
		if rle, ok := err.(*RateLimitError); ok {
			writeJSON(w, 429, map[string]interface{}{"error": map[string]string{
				"message": rle.Error(),
				"type":    "rate_limit_exceeded",
				"code":    "ip_slot_full",
			}})
			return
		}
		writeJSON(w, 502, map[string]interface{}{"error": map[string]string{"message": "upstream error: " + err.Error()}})
		return
	}

	cid := "chatcmpl-" + randHex(12)
	msg := map[string]interface{}{"role": "assistant"}
	if text != "" {
		msg["content"] = text
	} else {
		msg["content"] = nil
	}
	finish := "stop"
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
		finish = "tool_calls"
	}

	stream, _ := req["stream"].(bool)
	recordRequest("chat.completions", modelName, prompt, text, res, 200, "", stream)
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(200)
		chunk := map[string]interface{}{
			"id":      cid,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   modelName,
			"choices": []map[string]interface{}{{
				"index":         0,
				"delta":         msg,
				"finish_reason": finish,
			}},
		}
		chunkJSON, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"id":      cid,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   modelName,
		"choices": []map[string]interface{}{{
			"index":         0,
			"message":       msg,
			"finish_reason": finish,
		}},
		"usage": map[string]int{
			"prompt_tokens":     len(prompt) / 4,
			"completion_tokens": len(text) / 4,
			"total_tokens":      (len(prompt) + len(text)) / 4,
		},
	})
}

// handleResponses implements OpenAI's /v1/responses (Codex CLI format).
func handleResponses(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		return
	}
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"error": map[string]string{"message": "invalid JSON"}})
		return
	}

	modelInput := getStr(req, "model")
	if modelInput == "" {
		modelInput = cfg.DefaultModel
	}
	modelName, modelCfg, thinkMode, err := resolveModel(modelInput)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		return
	}

	var messages []map[string]interface{}
	if instr := getStr(req, "instructions"); instr != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": instr})
	}

	switch input := req["input"].(type) {
	case string:
		messages = append(messages, map[string]interface{}{"role": "user", "content": input})
	case []interface{}:
		for _, raw := range input {
			switch item := raw.(type) {
			case string:
				messages = append(messages, map[string]interface{}{"role": "user", "content": item})
			case map[string]interface{}:
				if t := getStr(item, "type"); t == "function_call_output" {
					messages = append(messages, map[string]interface{}{
						"role":         "tool",
						"tool_call_id": getStr(item, "call_id"),
						"name":         getStr(item, "name"),
						"content":      getStr(item, "output"),
					})
					continue
				}
				role := getStr(item, "role")
				if role == "assistant" || (getStr(item, "type") == "message" && role == "assistant") {
					var textAcc strings.Builder
					var tcList []map[string]interface{}
					if cp, ok := item["content"].([]interface{}); ok {
						for _, c := range cp {
							cm, ok := c.(map[string]interface{})
							if !ok {
								continue
							}
							switch getStr(cm, "type") {
							case "output_text":
								textAcc.WriteString(getStr(cm, "text"))
							case "function_call":
								tcList = append(tcList, cm)
							}
						}
					} else if s, ok := item["content"].(string); ok {
						textAcc.WriteString(s)
					}
					m := map[string]interface{}{"role": "assistant", "content": textAcc.String()}
					if len(tcList) > 0 {
						var tcs []map[string]interface{}
						for i, tc := range tcList {
							id := getStr(tc, "call_id")
							if id == "" {
								id = fmt.Sprintf("call_%d", i)
							}
							tcs = append(tcs, map[string]interface{}{
								"id":   id,
								"type": "function",
								"function": map[string]interface{}{
									"name":      getStr(tc, "name"),
									"arguments": getStr(tc, "arguments"),
								},
							})
						}
						m["tool_calls"] = tcs
					}
					messages = append(messages, m)
				} else {
					if role == "" {
						role = "user"
					}
					content := contentToString(item["content"])
					messages = append(messages, map[string]interface{}{"role": role, "content": content})
				}
			}
		}
	}

	toolsRaw, _ := req["tools"].([]interface{})
	var tools []map[string]interface{}
	for _, t := range toolsRaw {
		tm, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		// Normalize Responses API tool shape to Chat Completions shape.
		if getStr(tm, "type") == "function" && tm["function"] == nil {
			tools = append(tools, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        getStr(tm, "name"),
					"description": getStr(tm, "description"),
					"parameters":  tm["parameters"],
				},
			})
		} else {
			tools = append(tools, tm)
		}
	}

	prompt := messagesToPrompt(messages, tools)
	if strings.TrimSpace(prompt) == "" {
		writeJSON(w, 400, map[string]interface{}{"error": map[string]string{"message": "empty input"}})
		return
	}

	text, toolCalls, res, err := callGemini(prompt, modelCfg, thinkMode, tools)
	if err != nil {
		recordRequest("responses", modelName, prompt, "", nil, 502, err.Error(), false)
		if rle, ok := err.(*RateLimitError); ok {
			writeJSON(w, 429, map[string]interface{}{"error": map[string]string{
				"message": rle.Error(),
				"type":    "rate_limit_exceeded",
				"code":    "ip_slot_full",
			}})
			return
		}
		writeJSON(w, 502, map[string]interface{}{"error": map[string]string{"message": "upstream error: " + err.Error()}})
		return
	}

	rid := "resp_" + randHex(16)
	mid := "msg_" + randHex(12)
	var output []map[string]interface{}
	for _, tc := range toolCalls {
		output = append(output, map[string]interface{}{
			"type":      "function_call",
			"id":        tc.ID,
			"call_id":   tc.ID,
			"name":      tc.Function.Name,
			"arguments": tc.Function.Arguments,
			"status":    "completed",
		})
	}
	if text != "" || len(toolCalls) == 0 {
		output = append(output, map[string]interface{}{
			"type":   "message",
			"id":     mid,
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]interface{}{{
				"type":        "output_text",
				"text":        text,
				"annotations": []interface{}{},
			}},
		})
	}

	stream, _ := req["stream"].(bool)
	recordRequest("responses", modelName, prompt, text, res, 200, "", stream)
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		writeEvent := func(eventType string, payload interface{}) {
			pj, _ := json.Marshal(payload)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, pj)
			if flusher != nil {
				flusher.Flush()
			}
		}
		writeEvent("response.created", map[string]interface{}{
			"type": "response.created",
			"response": map[string]interface{}{
				"id":     rid,
				"object": "response",
				"status": "in_progress",
				"model":  modelName,
				"output": []interface{}{},
			},
		})
		for _, item := range output {
			switch item["type"] {
			case "function_call":
				writeEvent("response.function_call_arguments.done", map[string]interface{}{
					"type":      "response.function_call_arguments.done",
					"item_id":   item["id"],
					"call_id":   item["call_id"],
					"name":      item["name"],
					"arguments": item["arguments"],
				})
			case "message":
				if cps, ok := item["content"].([]map[string]interface{}); ok {
					for ci, cp := range cps {
						writeEvent("response.output_text.done", map[string]interface{}{
							"type":          "response.output_text.done",
							"item_id":       item["id"],
							"content_index": ci,
							"text":          cp["text"],
						})
					}
				}
			}
		}
		respObj := map[string]interface{}{
			"id":     rid,
			"object": "response",
			"status": "completed",
			"model":  modelName,
			"output": output,
			"usage": map[string]int{
				"input_tokens":  len(prompt) / 4,
				"output_tokens": len(text) / 4,
				"total_tokens":  (len(prompt) + len(text)) / 4,
			},
		}
		writeEvent("response.completed", map[string]interface{}{
			"type":     "response.completed",
			"response": respObj,
		})
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"id":         rid,
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     "completed",
		"model":      modelName,
		"output":     output,
		"usage": map[string]int{
			"input_tokens":  len(prompt) / 4,
			"output_tokens": len(text) / 4,
			"total_tokens":  (len(prompt) + len(text)) / 4,
		},
	})
}
