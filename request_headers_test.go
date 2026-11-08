package llmproxy

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestUpstreamRequestHeaders(t *testing.T) {
	in := http.Header{
		"authorization": {"Bearer caller"}, "Proxy-Authorization": {"Basic caller"},
		"Cookie": {"session=caller"}, "Cookie2": {"session=caller"},
		"X-Api-Key": {"caller"}, "Api-Key": {"caller"}, "X-Goog-Api-Key": {"caller"},
		"X-Amz-Security-Token": {"caller"}, "X-Unknown-Secret": {"caller"},
		"X-Forwarded-For": {"private"}, "Forwarded": {"private"}, "Baggage": {"private"},
		"Sec-Websocket-Protocol": {"bearer.caller"}, "Sec-Websocket-Key": {"caller"},
		"connection": {"keep-alive, X-Request-ID", "X-Client-Request-ID"},
		"Keep-Alive": {"timeout=5"}, "Transfer-Encoding": {"chunked"},
		"Te": {"trailers"}, "Trailer": {"X-Unknown-Secret"}, "Upgrade": {"websocket"},
		"Content-Length": {"999"}, "Host": {"caller.example"}, "Origin": {"https://caller.example"},
		"X-Provider": {"anthropic"}, "X-Request-Id": {"remove"},
		"X-Client-Request-Id": {"remove"},
		"Content-Type":        {"multipart/form-data; boundary=test"},
		"Accept":              {"application/json", "text/event-stream"},
		"Anthropic-Beta":      {"feature-one", "feature-two"},
		"Openai-Project":      {"project"}, "Openai-Beta": {"responses=experimental"},
		"X-Cache-Key": {"cache"}, "X-Org-Id": {"org"},
		"Idempotency-Key": {"request"},
	}
	before := cloneHeader(in)
	got := upstreamRequestHeaders(in, "anthropic")
	want := http.Header{
		"Content-Type":   {"multipart/form-data; boundary=test"},
		"Accept":         {"application/json", "text/event-stream"},
		"Anthropic-Beta": {"feature-one", "feature-two"},
		"X-Cache-Key":    {"cache"}, "X-Org-Id": {"org"},
		"Idempotency-Key": {"request"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwarded headers = %v; want %v", got, want)
	}
	got["Accept"][0] = "changed"
	if !reflect.DeepEqual(in, before) {
		t.Fatal("outbound headers modified the caller's headers")
	}
}

func TestUpstreamProviderOptions(t *testing.T) {
	options := map[string][]string{
		"openai":    {"Openai-Beta", "Openai-Organization", "Openai-Project"},
		"azure":     {"Openai-Beta", "Openai-Organization", "Openai-Project"},
		"anthropic": {"Anthropic-Beta", "Anthropic-Version"},
		"xai":       {"X-Grok-Conv-Id"},
		"fireworks": {"X-Session-Affinity", "X-Prompt-Cache-Isolation-Key"},
		"googleai":  {}, "bedrock": {}, "custom": {},
	}
	all := make(http.Header)
	for _, names := range options {
		for _, name := range names {
			all.Set(name, "option")
		}
	}
	for provider, names := range options {
		t.Run(provider, func(t *testing.T) {
			got := upstreamRequestHeaders(all, provider)
			want := make(http.Header)
			for _, name := range names {
				want.Set(name, "option")
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("options = %v; want %v", got, want)
			}
		})
	}
}

func TestForwardWebSocketStripsCallerCredentials(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)
	defer f.client.Close()
	defer f.upstream.Close()
	headers := http.Header{
		"Authorization": {"Bearer caller"}, "Cookie": {"session=caller"},
		"X-Api-Key": {"caller"}, "Api-Key": {"caller"}, "X-Goog-Api-Key": {"caller"},
		"X-Unknown-Secret":       {"caller"},
		"Sec-Websocket-Protocol": {"bearer.caller"}, "Sec-Websocket-Key": {"caller"},
		"Connection": {"Upgrade, X-Request-ID"}, "Upgrade": {"websocket"},
		"X-Request-Id": {"remove"}, "Openai-Beta": {"responses=experimental"},
		"Anthropic-Beta": {"remove"},
	}
	errCh := startForwardWS(t, f, headers)
	if err := f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)); err != nil {
		t.Fatal(err)
	}
	mustReadFrame(t, f.upstream)
	f.dialer.mu.Lock()
	got := cloneHeader(f.dialer.dialedHeader)
	f.dialer.mu.Unlock()
	want := http.Header{"Authorization": {"Bearer enriched"}, "Openai-Beta": {"responses=experimental"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dial headers = %v; want %v", got, want)
	}
	_ = f.client.Close()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("websocket relay did not stop")
	}
}
