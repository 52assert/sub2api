package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

const accountCLITestTimeout = 2 * time.Minute

// testOpenAICLIConnection reuses the isolated native CLI runner, including
// credential shadows, account model mappings, proxies and process cleanup.
func (s *AccountTestService) testOpenAICLIConnection(c *gin.Context, account *Account, modelID, prompt string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		modelID = openai.DefaultTestModel
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "Reply with OK. Do not use tools."
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{Type: "test_start", Model: account.GetMappedModel(modelID)})
	s.sendEvent(c, TestEvent{Type: "status", Text: "正在通过 Codex CLI 测试连接"})

	ctx, cancel := context.WithTimeout(c.Request.Context(), accountCLITestTimeout)
	defer cancel()
	var result IntelligenceTestGenerationResult
	var err error
	done := make(chan struct{})
	go func() {
		result, err = s.runCodexCLIIntelligenceTest(ctx, account.ID, modelID, prompt, "default")
		close(done)
	}()
	// CLI output is collected until the turn completes. Keep the SSE connection
	// active while reasoning so reverse proxies do not time out the request.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
waiting:
	for {
		select {
		case <-done:
			break waiting
		case <-ticker.C:
			if _, writeErr := fmt.Fprint(c.Writer, ": keepalive\n\n"); writeErr != nil {
				cancel()
				<-done
				return writeErr
			}
			c.Writer.Flush()
		}
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return s.sendErrorAndEnd(c, "Codex CLI connection test timed out after 2 minutes.")
		}
		return s.sendErrorAndEnd(c, fmt.Sprintf("Codex CLI connection test failed: %s", err))
	}
	output := result.FinalMessage
	if output == "" {
		output = result.Output
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: output})
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}
