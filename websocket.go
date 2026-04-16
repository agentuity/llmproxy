package llmproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// RFC 6455 WebSocket message type constants.
const (
	TextMessage   = 1
	BinaryMessage = 2
	CloseMessage  = 8
	PingMessage   = 9
	PongMessage   = 10
)

// WSConn abstracts a WebSocket connection for reading and writing messages.
//
// gorilla/websocket's *Conn satisfies this interface directly.
type WSConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
}

// WSUpgrader upgrades an HTTP request to a WebSocket connection.
// Consumers wrap their WebSocket library's upgrader to implement this.
type WSUpgrader interface {
	Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (WSConn, error)
}

// WSDialer dials a WebSocket connection to an upstream server.
// Consumers wrap their WebSocket library's dialer to implement this.
type WSDialer interface {
	DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (WSConn, *http.Response, error)
}

// WebSocketCapableProvider is implemented by providers that support WebSocket mode.
type WebSocketCapableProvider interface {
	Provider
	// WebSocketURL returns the upstream WebSocket URL for this provider.
	WebSocketURL(meta BodyMetadata) (*url.URL, error)
}

// WSEventCallback is an optional callback for WebSocket events.
// usage is non-nil for response.completed events that include usage data.
type WSEventCallback func(eventType string, data []byte, usage *StreamingUsage)

// WSBillingCallback is invoked per completed response turn.
type WSBillingCallback func(turn int, meta ResponseMetadata, billing *BillingResult)

// WSMessage is a lightweight parsed view of a WebSocket JSON message.
type WSMessage struct {
	Type               string          `json:"type"`
	Model              string          `json:"model,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Raw                json.RawMessage `json:"-"`
}

// ParseWSMessage parses a WebSocket JSON message and extracts commonly used fields.
func ParseWSMessage(data []byte) (*WSMessage, error) {
	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	msg.Raw = append(json.RawMessage(nil), data...)
	return &msg, nil
}

// WSResponseCompleted is the minimal shape needed to extract usage from
// OpenAI Responses API WebSocket response.completed events.
type WSResponseCompleted struct {
	Type     string              `json:"type"`
	Response *WSResponseEnvelope `json:"response,omitempty"`
	Usage    *WSResponseUsage    `json:"usage,omitempty"`
}

type WSResponseEnvelope struct {
	Usage *WSResponseUsage `json:"usage,omitempty"`
}

type WSResponseUsage struct {
	InputTokens         int                      `json:"input_tokens"`
	OutputTokens        int                      `json:"output_tokens"`
	TotalTokens         int                      `json:"total_tokens"`
	InputTokensDetails  *WSResponseInputDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *WSResponseOutputDetails `json:"output_tokens_details,omitempty"`
}

type WSResponseInputDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type WSResponseOutputDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// ExtractWSUsage extracts usage from a response.completed WebSocket message.
// Returns nil,nil for non-response.completed events.
func ExtractWSUsage(data []byte) (*StreamingUsage, error) {
	var msg WSResponseCompleted
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}

	if msg.Type != "response.completed" {
		return nil, nil
	}

	usage := msg.Usage
	if usage == nil && msg.Response != nil {
		usage = msg.Response.Usage
	}
	if usage == nil {
		return nil, nil
	}

	out := &StreamingUsage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
	}

	if usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > 0 {
		out.CacheUsage = &CacheUsage{CachedTokens: usage.InputTokensDetails.CachedTokens}
	}

	if usage.OutputTokensDetails != nil && usage.OutputTokensDetails.ReasoningTokens > 0 {
		out.ReasoningTokens = usage.OutputTokensDetails.ReasoningTokens
	}

	return out, nil
}
