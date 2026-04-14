package llmproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var (
	ErrStreamComplete = errors.New("stream complete")
)

type SSEEvent struct {
	ID    []byte
	Event []byte
	Data  []byte
	Retry []byte
}

type SSEParser struct {
	scanner *bufio.Scanner
}

func NewSSEParser(r io.Reader) *SSEParser {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	return &SSEParser{
		scanner: scanner,
	}
}

func (p *SSEParser) Next() (*SSEEvent, error) {
	var event SSEEvent

	for p.scanner.Scan() {
		line := p.scanner.Bytes()

		if len(line) == 0 {
			if len(event.Data) > 0 {
				return &event, nil
			}
			continue
		}

		if line[0] == ':' {
			continue
		}

		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			event.Data = append(event.Data, line...)
			continue
		}

		field := line[:colon]
		value := line[colon+1:]

		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}

		switch string(field) {
		case "id":
			event.ID = append(event.ID[:0], value...)
		case "event":
			event.Event = append(event.Event[:0], value...)
		case "data":
			if len(event.Data) > 0 {
				event.Data = append(event.Data, '\n')
			}
			event.Data = append(event.Data, value...)
		case "retry":
			event.Retry = append(event.Retry[:0], value...)
		}
	}

	if err := p.scanner.Err(); err != nil {
		return nil, err
	}

	if len(event.Data) > 0 {
		return &event, nil
	}

	return nil, io.EOF
}

type StreamingUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CacheUsage       *CacheUsage
}

type OpenAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []OpenAIStreamChoice `json:"choices"`
	Usage   *OpenAIStreamUsage   `json:"usage,omitempty"`
}

type OpenAIStreamChoice struct {
	Index        int                   `json:"index"`
	Delta        *OpenAIStreamDelta    `json:"delta,omitempty"`
	FinishReason string                `json:"finish_reason,omitempty"`
	Logprobs     *OpenAIStreamLogprobs `json:"logprobs,omitempty"`
}

type OpenAIStreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type OpenAIStreamLogprobs struct {
	Content []OpenAIStreamLogprobContent `json:"content,omitempty"`
}

type OpenAIStreamLogprobContent struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
}

type OpenAIStreamUsage struct {
	PromptTokens            int                            `json:"prompt_tokens"`
	CompletionTokens        int                            `json:"completion_tokens"`
	TotalTokens             int                            `json:"total_tokens"`
	PromptTokensDetails     *OpenAIStreamPromptDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *OpenAIStreamCompletionDetails `json:"completion_tokens_details,omitempty"`
}

type OpenAIStreamPromptDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
	AudioTokens  int `json:"audio_tokens,omitempty"`
}

type OpenAIStreamCompletionDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	AudioTokens              int `json:"audio_tokens,omitempty"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

func ParseOpenAISSEEvent(data []byte) (*OpenAIStreamChunk, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}

	if bytes.Equal(data, []byte("[DONE]")) {
		return nil, ErrStreamComplete
	}

	var chunk OpenAIStreamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil, err
	}

	return &chunk, nil
}

type AnthropicStreamEvent struct {
	Type         string                  `json:"type"`
	Index        int                     `json:"index,omitempty"`
	Delta        *AnthropicStreamDelta   `json:"delta,omitempty"`
	ContentBlock *AnthropicContentBlock  `json:"content_block,omitempty"`
	Usage        *AnthropicStreamUsage   `json:"usage,omitempty"`
	Message      *AnthropicStreamMessage `json:"message,omitempty"`
}

type AnthropicStreamDelta struct {
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

type AnthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type AnthropicStreamUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

type AnthropicStreamMessage struct {
	ID         string                  `json:"id,omitempty"`
	Type       string                  `json:"type,omitempty"`
	Role       string                  `json:"role,omitempty"`
	Content    []AnthropicContentBlock `json:"content,omitempty"`
	Model      string                  `json:"model,omitempty"`
	StopReason string                  `json:"stop_reason,omitempty"`
	Usage      *AnthropicStreamUsage   `json:"usage,omitempty"`
}

func ParseAnthropicSSEEvent(data []byte) (*AnthropicStreamEvent, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}

	var event AnthropicStreamEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}

	return &event, nil
}

func IsSSEStream(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}

func ExtractUsageFromOpenAIChunk(chunk *OpenAIStreamChunk) *StreamingUsage {
	if chunk == nil || chunk.Usage == nil {
		return nil
	}

	usage := &StreamingUsage{
		PromptTokens:     chunk.Usage.PromptTokens,
		CompletionTokens: chunk.Usage.CompletionTokens,
		TotalTokens:      chunk.Usage.TotalTokens,
	}

	if chunk.Usage.PromptTokensDetails != nil && chunk.Usage.PromptTokensDetails.CachedTokens > 0 {
		usage.CacheUsage = &CacheUsage{
			CachedTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
		}
	}

	return usage
}

func ExtractUsageFromAnthropicEvent(event *AnthropicStreamEvent) *StreamingUsage {
	if event == nil {
		return nil
	}

	var usage *AnthropicStreamUsage

	switch event.Type {
	case "message_start":
		if event.Message != nil && event.Message.Usage != nil {
			usage = event.Message.Usage
		}
	case "message_delta":
		if event.Usage != nil {
			usage = event.Usage
		}
	case "message_stop":
		return nil
	default:
		return nil
	}

	if usage == nil {
		return nil
	}

	result := &StreamingUsage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
	}

	if usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0 {
		result.CacheUsage = &CacheUsage{
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
		}
	}

	return result
}

func FormatSSEEvent(event string, data []byte) []byte {
	var buf bytes.Buffer
	if len(event) > 0 {
		buf.WriteString("event: ")
		buf.WriteString(event)
		buf.WriteByte('\n')
	}
	// Split data on newlines and write each as a separate "data:" line
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		buf.WriteString("data: ")
		buf.Write(line)
		buf.WriteByte('\n')
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}
