package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const intelligenceMaxOutputBytes = 2 << 20
const intelligenceMaxTokens = 32768

type accountIntelligenceContextKey struct{}
type accountIntelligenceRequest struct {
	prompt    string
	effort    string
	maxTokens int
}

func intelligenceRequest(ctx context.Context) *accountIntelligenceRequest {
	request, _ := ctx.Value(accountIntelligenceContextKey{}).(*accountIntelligenceRequest)
	return request
}

func accountTestUpstreamProfile(ctx context.Context, profile HTTPUpstreamProfile) context.Context {
	if intelligenceRequest(ctx) != nil {
		return WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileLongStream)
	}
	return WithHTTPUpstreamProfile(ctx, profile)
}

// ValidateIntelligenceTest applies the same model and reasoning checks used by
// generation, so invalid choices can be rejected before scheduling a task.
func (s *AccountTestService) ValidateIntelligenceTest(account *Account, model, effort string) (err error) {
	defer func() {
		if err != nil {
			err = infraerrors.BadRequest("INVALID_INTELLIGENCE_TEST", err.Error())
		}
	}()
	if account == nil {
		return errors.New("Account not found")
	}
	model = strings.TrimSpace(model)
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "default" {
		effort = ""
	}
	if model == "" {
		return errors.New("请选择测试模型")
	}
	switch effort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		return errors.New("不支持的思考等级")
	}
	if account.IsSyntheticUITest() {
		return errors.New("模拟账号不支持降智测试")
	}
	if (account.Platform != PlatformAntigravity || account.Type != AccountTypeOAuth) && !account.IsModelSupported(model) {
		return errors.New("该账号不支持所选模型")
	}
	mapped := account.GetMappedModel(model)
	if s != nil && account.Platform == PlatformAntigravity && account.Type != AccountTypeAPIKey && account.Type != AccountTypeUpstream && s.antigravityGatewayService != nil {
		mapped = s.antigravityGatewayService.getMappedModel(account, model)
		if strings.HasPrefix(strings.ToLower(mapped), "gemini-3") && containsIntelligenceEffort([]string{"low", "medium", "high"}, effort) {
			mapped = s.antigravityGatewayService.getMappedModelForThinkingLevel(account, model, effort)
		}
		if mapped == "" {
			return errors.New("该账号不支持所选模型")
		}
	}
	if intelligenceMediaModel(mapped) {
		return errors.New("降智测试只支持文本模型，请选择可生成 HTML / SVG 的模型")
	}
	protocol := "anthropic"
	switch {
	case account.Platform == PlatformAntigravity && account.Type != AccountTypeAPIKey && account.Type != AccountTypeUpstream:
		if strings.HasPrefix(mapped, "gemini-") {
			protocol = "gemini"
		} else {
			protocol = "antigravity-claude"
		}
	case account.IsGemini() || account.Platform == PlatformAntigravity && account.Type == AccountTypeAPIKey && strings.HasPrefix(mapped, "gemini-"):
		protocol = "gemini"
	case account.Platform == PlatformGrok:
		protocol = "responses"
	case account.IsOpenCodeGo():
		proto := account.GetAPIProtocol()
		if proto != APIProtocolAnthropic && proto != APIProtocolChatCompletions && proto != APIProtocolResponses {
			proto = openCodeGoNativeProtocol(account, mapped)
		}
		if proto == APIProtocolResponses {
			protocol = "responses"
		} else if proto != APIProtocolAnthropic {
			protocol = "chat"
		}
	case account.IsCNProvider():
		if account.GetAPIProtocol() == APIProtocolResponses {
			protocol = "responses"
		} else if account.GetAPIProtocol() != APIProtocolAnthropic {
			protocol = "chat"
		}
	case account.IsOpenAI():
		protocol = "responses"
		if account.Type == AccountTypeAPIKey && !openai_compat.ShouldUseResponsesAPI(account.Extra) {
			protocol = "chat"
		}
	}
	if account.IsBedrock() {
		protocol = "bedrock"
		var ok bool
		mapped, ok = ResolveBedrockModelID(account, model)
		if !ok {
			return errors.New("不支持的 Bedrock 模型")
		}
	}
	if effort != "" {
		if metadata, ok := intelligenceModelMetadata(account, model, mapped); ok {
			if metadata.Reasoning != nil && !*metadata.Reasoning {
				return fmt.Errorf("模型 %s 不支持思考等级，请选择默认", mapped)
			}
			if len(metadata.SupportedReasoningLevels) > 0 && !containsIntelligenceEffort(metadata.SupportedReasoningLevels, effort) {
				return fmt.Errorf("模型 %s 不支持思考等级 %s", mapped, effort)
			}
			if metadata.MaxOutputTokens > 0 && effort != "none" && (protocol == "antigravity-claude" || protocol == "bedrock" && !claude.IsSonnet55(mapped) || protocol == "anthropic" && len(claude.EffortLevelsForModel(mapped)) == 0) && metadata.MaxOutputTokens <= int64(intelligenceThinkingBudget(effort)) {
				return fmt.Errorf("模型 %s 的输出限制不足以应用思考等级 %s，请选择较低等级或默认", mapped, effort)
			}
		}
	}
	return validateIntelligenceEffort(protocol, mapped, effort)
}

func intelligenceModelMetadata(account *Account, selected, mapped string) (UpstreamModelMetadata, bool) {
	if metadata, ok := account.GetUpstreamModelMetadata(mapped); ok {
		return metadata, true
	}
	return account.GetUpstreamModelMetadata(selected)
}

func intelligenceOutputTokenBudget(account *Account, selected string) int {
	if metadata, ok := intelligenceModelMetadata(account, selected, account.GetMappedModel(selected)); ok && metadata.MaxOutputTokens > 0 && metadata.MaxOutputTokens < intelligenceMaxTokens {
		return int(metadata.MaxOutputTokens)
	}
	return intelligenceMaxTokens
}

func intelligenceMediaModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return isOpenAIImageModel(model) || isImageGenerationModel(model) || isGrokImageGenerationModel(model) || isGrokVideoGenerationModel(model) ||
		strings.Contains(model, "audio") || strings.Contains(model, "realtime") || strings.Contains(model, "voice") || strings.Contains(model, "embedding") ||
		strings.Contains(model, "tts") || strings.Contains(model, "whisper") || strings.Contains(model, "imagen") || strings.HasPrefix(model, "veo-")
}

func containsIntelligenceEffort(levels []string, effort string) bool {
	for _, level := range levels {
		if level == effort {
			return true
		}
	}
	return false
}

func validateIntelligenceEffort(protocol, model, effort string) error {
	if effort == "" {
		return nil
	}
	unsupported := func() error {
		return fmt.Errorf("模型 %s 在 %s 协议下不支持思考等级 %s，请选择默认或受支持的等级", model, protocol, effort)
	}
	lower := strings.TrimPrefix(strings.ToLower(model), "models/")
	switch protocol {
	case "bedrock":
		if claude.IsSonnet55(model) {
			return validateIntelligenceEffort("anthropic", model, effort)
		}
		if isBedrockOpus47OrNewer(model) || strings.Contains(lower, "fable-5") || strings.Contains(lower, "mythos-5") {
			return unsupported()
		}
		if !containsIntelligenceEffort([]string{"none", "low", "medium", "high"}, effort) || (!strings.Contains(lower, "claude-3-7") && !strings.Contains(lower, "claude-sonnet-4") && !strings.Contains(lower, "claude-opus-4") && !strings.Contains(lower, "claude-haiku-4-5")) {
			return unsupported()
		}
	case "anthropic":
		levels := claude.EffortLevelsForModel(model)
		if len(levels) > 0 {
			if !containsIntelligenceEffort(levels, effort) {
				return unsupported()
			}
		} else if strings.Contains(lower, "claude-") {
			if !containsIntelligenceEffort([]string{"none", "low", "medium", "high"}, effort) {
				return unsupported()
			}
			if !strings.Contains(lower, "claude-3-7") && !strings.Contains(lower, "claude-sonnet-4") && !strings.Contains(lower, "claude-opus-4") && !strings.Contains(lower, "claude-haiku-4-5") {
				return unsupported()
			}
		} else {
			return unsupported()
		}
	case "antigravity-claude":
		if !containsIntelligenceEffort([]string{"none", "low", "medium", "high"}, effort) {
			return unsupported()
		}
		if (claude.IsOpus55(model) || claude.IsSonnet55(model)) && effort == "none" {
			return unsupported()
		}
	case "gemini":
		if strings.HasPrefix(lower, "gemini-3") {
			if !containsIntelligenceEffort([]string{"minimal", "low", "medium", "high"}, effort) {
				return unsupported()
			}
			if strings.Contains(lower, "pro") && (effort == "minimal" || effort == "medium") {
				return unsupported()
			}
			for _, level := range []string{"low", "medium", "high"} {
				if strings.HasSuffix(lower, "-"+level) && level != effort {
					return unsupported()
				}
			}
		} else if strings.HasPrefix(lower, "gemini-2.5") {
			if !containsIntelligenceEffort([]string{"none", "low", "medium", "high"}, effort) || strings.Contains(lower, "pro") && effort == "none" {
				return unsupported()
			}
		} else {
			return unsupported()
		}
	default:
		if err := openai.ValidateGPT61SolReasoningEffort(model, effort); err != nil {
			return err
		}
		if effort == "max" && !supportsOpenAIReasoningEffortMax(model) {
			return unsupported()
		}
		if strings.Contains(lower, "deepseek-v4") || strings.HasPrefix(lower, "deepseek-flash") {
			if !containsIntelligenceEffort([]string{"none", "low", "high", "max"}, effort) {
				return unsupported()
			}
		}
		if strings.HasPrefix(lower, "glm-") {
			levels := []string{"high", "max"}
			if isGLM53Model(lower) {
				levels = append(levels, "low")
			}
			if !containsIntelligenceEffort(levels, effort) {
				return unsupported()
			}
		}
	}
	return nil
}

// RunIntelligenceTest reuses account connectivity routing, authentication,
// proxies and model mappings, but opts into generation budgets and strict
// completion checks. It retains only final text, with a bounded in-memory sink.
func (s *AccountTestService) RunIntelligenceTest(ctx context.Context, accountID int64, model, prompt, reasoningEffort string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	parentContext := ctx
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("请输入测试提示词")
	}
	if len(prompt) > 64<<10 {
		return "", errors.New("测试提示词不能超过 64 KiB")
	}
	if s == nil || s.accountRepo == nil {
		return "", errors.New("Account test service is unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return "", err
	}
	if err := s.ValidateIntelligenceTest(account, model, reasoningEffort); err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	effort := strings.ToLower(strings.TrimSpace(reasoningEffort))
	if effort == "default" {
		effort = ""
	}
	ctx = context.WithValue(ctx, accountIntelligenceContextKey{}, &accountIntelligenceRequest{prompt: prompt, effort: effort, maxTokens: intelligenceOutputTokenBudget(account, strings.TrimSpace(model))})
	ctx = WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileLongStream)
	writer := &intelligenceCaptureWriter{header: make(http.Header), cancel: cancel}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/intelligence-test", nil).WithContext(ctx)
	err = s.TestAccountConnection(c, accountID, strings.TrimSpace(model), prompt, AccountTestModeDefault)
	if err := parentContext.Err(); err != nil {
		return "", err
	}
	if writer.err != nil {
		return "", writer.err
	}
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !writer.complete {
		return "", errors.New("测试响应未完整结束")
	}
	if strings.TrimSpace(writer.output.String()) == "" {
		return "", errors.New("模型未返回可展示的内容")
	}
	return writer.output.String(), nil
}

type intelligenceCaptureWriter struct {
	header   http.Header
	output   strings.Builder
	complete bool
	err      error
	cancel   context.CancelFunc
}

func (w *intelligenceCaptureWriter) Header() http.Header { return w.header }
func (w *intelligenceCaptureWriter) WriteHeader(int)     {}
func (w *intelligenceCaptureWriter) Flush()              {}
func (w *intelligenceCaptureWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if len(data) > intelligenceMaxOutputBytes*6 {
		w.fail(errors.New("测试输出超过 2 MiB 限制"))
		return 0, w.err
	}
	var event TestEvent
	encoded := bytes.TrimSpace(data)
	encoded = bytes.TrimSpace(bytes.TrimPrefix(encoded, []byte("data:")))
	if err := json.Unmarshal(encoded, &event); err != nil {
		w.fail(fmt.Errorf("读取测试结果失败: %w", err))
		return 0, w.err
	}
	switch event.Type {
	case "content":
		if w.output.Len()+len(event.Text) > intelligenceMaxOutputBytes {
			w.fail(errors.New("测试输出超过 2 MiB 限制"))
			return 0, w.err
		}
		_, _ = w.output.WriteString(event.Text)
	case "error":
		message := event.Error
		if len(message) > 16<<10 {
			message = message[:16<<10]
		}
		w.fail(errors.New(message))
	case "test_complete":
		w.complete = event.Success
	}
	return len(data), nil
}
func (w *intelligenceCaptureWriter) fail(err error) {
	w.err = err
	if w.cancel != nil {
		w.cancel()
	}
}

func applyIntelligencePayload(ctx context.Context, protocol, model string, payload map[string]any, isOAuth bool) error {
	request := intelligenceRequest(ctx)
	if request == nil {
		return nil
	}
	if intelligenceMediaModel(model) {
		return errors.New("降智测试只支持文本模型")
	}
	if err := validateIntelligenceEffort(protocol, model, request.effort); err != nil {
		return err
	}
	effort := request.effort
	maxTokens := request.maxTokens
	if maxTokens <= 0 {
		maxTokens = intelligenceMaxTokens
	}
	switch protocol {
	case "anthropic", "antigravity-claude", "bedrock":
		payload["messages"] = []map[string]any{{"role": "user", "content": []map[string]any{{"type": "text", "text": request.prompt}}}}
		payload["max_tokens"] = maxTokens
		delete(payload, "temperature")
		if effort == "" {
			return nil
		}
		if (protocol == "anthropic" || protocol == "bedrock" && claude.IsSonnet55(model)) && len(claude.EffortLevelsForModel(model)) > 0 {
			payload["output_config"] = map[string]any{"effort": effort}
			if strings.Contains(model, "opus-4-5") {
				payload["thinking"] = map[string]any{"type": "enabled", "budget_tokens": intelligenceThinkingBudget(effort)}
			} else {
				payload["thinking"] = map[string]any{"type": "adaptive"}
			}
		} else if effort == "none" {
			payload["thinking"] = map[string]any{"type": "disabled"}
		} else {
			payload["thinking"] = map[string]any{"type": "enabled", "budget_tokens": intelligenceThinkingBudget(effort)}
		}
	case "gemini":
		payload["contents"] = []map[string]any{{"role": "user", "parts": []map[string]any{{"text": request.prompt}}}}
		generation := map[string]any{"maxOutputTokens": maxTokens}
		if effort != "" {
			thinking := map[string]any{"includeThoughts": false}
			if strings.HasPrefix(strings.TrimPrefix(strings.ToLower(model), "models/"), "gemini-3") {
				thinking["thinkingLevel"] = effort
			} else if effort == "none" {
				thinking["thinkingBudget"] = 0
			} else {
				thinking["thinkingBudget"] = intelligenceThinkingBudget(effort)
			}
			generation["thinkingConfig"] = thinking
		}
		payload["generationConfig"] = generation
	case "chat":
		payload["messages"] = []map[string]any{{"role": "user", "content": request.prompt}}
		lower := strings.ToLower(model)
		if strings.HasPrefix(lower, "gpt-5") || strings.HasPrefix(lower, "gpt-6") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4") {
			payload["max_completion_tokens"] = maxTokens
		} else {
			payload["max_tokens"] = maxTokens
		}
		if effort != "" {
			payload["reasoning_effort"] = effort
		}
		if strings.Contains(lower, "deepseek-v4") || strings.HasPrefix(lower, "deepseek-flash") {
			if effort == "none" {
				payload["thinking"] = map[string]any{"type": "disabled"}
				delete(payload, "reasoning_effort")
			} else if effort != "" {
				payload["thinking"] = map[string]any{"type": "enabled"}
			}
		}
	case "responses":
		payload["input"] = []map[string]any{{"role": "user", "content": []map[string]any{{"type": "input_text", "text": request.prompt}}}}
		// ChatGPT Codex explicitly rejects max_output_tokens; its native model
		// output budget is used rather than sending an unsupported API field.
		if !isOAuth {
			payload["max_output_tokens"] = maxTokens
		}
		if effort != "" {
			payload["reasoning"] = map[string]any{"effort": effort}
		}
	}
	return nil
}

func intelligenceThinkingBudget(effort string) int {
	switch effort {
	case "low":
		return 1024
	case "medium":
		return 8192
	default:
		return 16384
	}
}

// processIntelligenceStream requires a provider completion marker. EOF,
// token-limit termination and blocked output never become successful records.
func (s *AccountTestService) processIntelligenceStream(c *gin.Context, body io.Reader, protocol string) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), intelligenceMaxOutputBytes+64<<10)
	var dataLines []string
	dataBytes := 0
	seenFinish := false
	seenText := false
	stopReason := ""
	process := func(raw string) (bool, error) {
		raw = strings.TrimSpace(raw)
		if raw == "[DONE]" {
			if protocol == "chat" && seenFinish {
				return true, nil
			}
			return false, errors.New("测试流在完整完成事件之前结束")
		}
		if !gjson.Valid(raw) {
			return false, errors.New("测试流包含无效 JSON")
		}
		data := gjson.Parse(raw)
		if upstreamError := data.Get("error"); upstreamError.Exists() && upstreamError.Type != gjson.Null {
			message := upstreamError.Get("message").String()
			if message == "" {
				message = "上游返回错误"
			}
			return false, errors.New(message)
		}
		emit := func(text string) {
			if text != "" {
				seenText = true
				s.sendEvent(c, TestEvent{Type: "content", Text: text})
			}
		}
		switch protocol {
		case "responses":
			switch data.Get("type").String() {
			case "response.output_text.delta":
				emit(data.Get("delta").String())
			case "response.completed", "response.done":
				response := data.Get("response")
				if status := response.Get("status").String(); status != "" && status != "completed" {
					return false, fmt.Errorf("测试响应未完成: %s", status)
				}
				if response.Get("error").Type != gjson.Null && response.Get("error").Exists() {
					return false, errors.New("上游测试响应返回错误")
				}
				if !seenText {
					for _, item := range response.Get("output").Array() {
						for _, part := range item.Get("content").Array() {
							if part.Get("type").String() == "output_text" {
								emit(part.Get("text").String())
							}
						}
					}
				}
				return true, nil
			case "response.failed", "response.incomplete", "response.cancelled", "error":
				return false, errors.New("上游测试响应失败或未完成")
			}
		case "chat":
			for _, choice := range data.Get("choices").Array() {
				if index := choice.Get("index"); index.Exists() && index.Int() != 0 {
					continue
				}
				emit(choice.Get("delta.content").String())
				if finish := choice.Get("finish_reason").String(); finish != "" {
					if finish != "stop" {
						return false, fmt.Errorf("测试生成未完整结束: %s", finish)
					}
					seenFinish = true
				}
			}
		case "anthropic":
			switch data.Get("type").String() {
			case "content_block_delta":
				if data.Get("delta.type").String() == "text_delta" || data.Get("delta.type").String() == "" {
					emit(data.Get("delta.text").String())
				}
			case "content_block_start":
				if data.Get("content_block.type").String() == "text" {
					emit(data.Get("content_block.text").String())
				}
			case "message_delta":
				stopReason = data.Get("delta.stop_reason").String()
			case "message_stop":
				if stopReason != "end_turn" && stopReason != "stop_sequence" {
					return false, fmt.Errorf("测试生成未完整结束: %s", stopReason)
				}
				return true, nil
			}
		case "gemini":
			if response := data.Get("response"); response.Exists() {
				data = response
			}
			if data.Get("error").Exists() {
				return false, errors.New("上游 Gemini 测试响应错误")
			}
			if reason := data.Get("promptFeedback.blockReason").String(); reason != "" {
				return false, fmt.Errorf("测试提示词被拦截: %s", reason)
			}
			for _, candidate := range data.Get("candidates").Array() {
				if index := candidate.Get("index"); index.Exists() && index.Int() != 0 {
					continue
				}
				for _, part := range candidate.Get("content.parts").Array() {
					if !part.Get("thought").Bool() {
						emit(part.Get("text").String())
					}
				}
				if finish := candidate.Get("finishReason").String(); finish != "" {
					if finish != "STOP" {
						return false, fmt.Errorf("测试生成未完整结束: %s", finish)
					}
					return true, nil
				}
			}
		}
		if err := c.Request.Context().Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	flush := func() (bool, error) {
		if len(dataLines) == 0 {
			return false, nil
		}
		raw := strings.Join(dataLines, "\n")
		dataLines = nil
		dataBytes = 0
		return process(raw)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			done, err := flush()
			if err != nil {
				return s.sendErrorAndEnd(c, err.Error())
			}
			if done {
				s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataBytes += len(line)
			if dataBytes > intelligenceMaxOutputBytes+64<<10 {
				return s.sendErrorAndEnd(c, "测试流单个事件超过大小限制")
			}
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("读取测试流失败: %s", err))
	}
	if done, err := flush(); err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	} else if done {
		s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
		return nil
	}
	if protocol == "chat" && seenFinish {
		s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
		return nil
	}
	return s.sendErrorAndEnd(c, "测试流在完整完成事件之前结束")
}

func (s *AccountTestService) runAntigravityIntelligenceGeneration(c *gin.Context, account *Account, model string) error {
	ctx := c.Request.Context()
	gateway := s.antigravityGatewayService
	if gateway == nil || gateway.tokenProvider == nil {
		return s.sendErrorAndEnd(c, "Antigravity token provider not configured")
	}
	request := intelligenceRequest(ctx)
	projectID, err := resolveAntigravityProjectID(account)
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	mapped := gateway.getMappedModel(account, model)
	if strings.HasPrefix(strings.ToLower(mapped), "gemini-3") && containsIntelligenceEffort([]string{"low", "medium", "high"}, request.effort) {
		mapped = gateway.getMappedModelForThinkingLevel(account, model, request.effort)
	}
	if mapped == "" {
		return s.sendErrorAndEnd(c, "该账号不支持所选模型")
	}
	var body []byte
	if strings.HasPrefix(mapped, "gemini-") {
		payload := map[string]any{}
		if err := applyIntelligencePayload(ctx, "gemini", mapped, payload, false); err != nil {
			return s.sendErrorAndEnd(c, err.Error())
		}
		encoded, _ := json.Marshal(payload)
		if request.effort != "" {
			mapped = gateway.getMappedModelForThinkingLevel(account, model, request.effort)
			if mapped == "" {
				return s.sendErrorAndEnd(c, "该账号不支持所选思考等级")
			}
			if err := validateIntelligenceEffort("gemini", mapped, request.effort); err != nil {
				return s.sendErrorAndEnd(c, err.Error())
			}
		}
		encoded, err = injectIdentityPatchToGeminiRequest(encoded)
		if err == nil {
			body, err = gateway.wrapV1InternalRequest(projectID, mapped, encoded)
		}
	} else {
		payload := map[string]any{"model": mapped, "stream": true}
		if err := applyIntelligencePayload(ctx, "antigravity-claude", mapped, payload, false); err != nil {
			return s.sendErrorAndEnd(c, err.Error())
		}
		encoded, _ := json.Marshal(payload)
		var claudeRequest antigravity.ClaudeRequest
		if err = json.Unmarshal(encoded, &claudeRequest); err == nil {
			mapped = applyThinkingModelSuffix(mapped, request.effort != "" && request.effort != "none")
			opts := gateway.getClaudeTransformOptions(ctx)
			opts.EnableIdentityPatch = true
			body, err = antigravity.TransformClaudeToGeminiWithOptions(&claudeRequest, projectID, mapped, opts)
		}
	}
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("构建 Antigravity 测试请求失败: %s", err))
	}
	token, err := gateway.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("获取 access_token 失败: %s", err))
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	s.sendEvent(c, TestEvent{Type: "test_start", Model: model})
	result, err := gateway.antigravityRetryLoop(antigravityRetryLoopParams{ctx: ctx, prefix: fmt.Sprintf("[antigravity-Intelligence] account=%d", account.ID), account: account, proxyURL: proxyURL, accessToken: token, action: "streamGenerateContent", body: body, c: nil, httpUpstream: gateway.httpUpstream, settingService: gateway.settingService, accountRepo: gateway.accountRepo, requestedModel: model, handleError: testConnectionHandleError})
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	if result == nil || result.resp == nil {
		return s.sendErrorAndEnd(c, "upstream returned empty response")
	}
	defer func() { _ = result.resp.Body.Close() }()
	if result.resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(result.resp.Body, 16<<10))
		return s.sendErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", result.resp.StatusCode, body))
	}
	return s.processIntelligenceStream(c, result.resp.Body, "gemini")
}

func (s *AccountTestService) processIntelligenceClaudeResponse(c *gin.Context, body []byte) error {
	if !gjson.ValidBytes(body) {
		return s.sendErrorAndEnd(c, "Invalid Claude generation response")
	}
	data := gjson.ParseBytes(body)
	if data.Get("error").Exists() {
		return s.sendErrorAndEnd(c, "Claude generation response returned an error")
	}
	reason := data.Get("stop_reason").String()
	if reason != "end_turn" && reason != "stop_sequence" {
		return s.sendErrorAndEnd(c, fmt.Sprintf("测试生成未完整结束: %s", reason))
	}
	for _, block := range data.Get("content").Array() {
		if block.Get("type").String() == "text" {
			s.sendEvent(c, TestEvent{Type: "content", Text: block.Get("text").String()})
		}
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

// Upstream Antigravity accounts use the same dual authentication headers as
// ForwardUpstream while retaining URL validation and strict Claude completion.
func (s *AccountTestService) runAntigravityUpstreamIntelligenceGeneration(c *gin.Context, account *Account, model string) error {
	ctx := c.Request.Context()
	baseURL, err := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err))
	}
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return s.sendErrorAndEnd(c, "No API key available")
	}
	model = account.GetMappedModel(model)
	payload := map[string]any{"model": model, "stream": true}
	if err := applyIntelligencePayload(ctx, "anthropic", model, payload, false); err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	account.ApplyHeaderOverrides(req.Header)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	s.sendEvent(c, TestEvent{Type: "test_start", Model: model})
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		return s.sendErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, body))
	}
	return s.processIntelligenceStream(c, resp.Body, "anthropic")
}
