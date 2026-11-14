package llmproxy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/agentuity/llmproxy"
	"github.com/agentuity/llmproxy/providers/anthropic"
	"github.com/agentuity/llmproxy/providers/azure"
	"github.com/agentuity/llmproxy/providers/bedrock"
	"github.com/agentuity/llmproxy/providers/googleai"
	"github.com/agentuity/llmproxy/providers/openai"
)

type headerCaptureTransport struct {
	header http.Header
}

func (c *headerCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.header = req.Header.Clone()
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader("{}")),
	}, nil
}

func TestCredentialBoundaryWithRealProviders(t *testing.T) {
	o, err := openai.New("fixture-provider")
	if err != nil {
		t.Fatal(err)
	}
	a, err := anthropic.New("fixture-provider")
	if err != nil {
		t.Fatal(err)
	}
	g, err := googleai.New("fixture-provider")
	if err != nil {
		t.Fatal(err)
	}
	z, err := azure.New("fixture", "deployment", azure.DefaultAPIVersion(), azure.WithAPIKey("fixture-provider"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := bedrock.New("us-east-1", "fixture-access", "fixture-secret", "fixture-session")
	if err != nil {
		t.Fatal(err)
	}
	providers := []struct {
		provider llmproxy.Provider
		authName string
		auth     string
		body     string
	}{
		{o, "Authorization", "Bearer fixture-provider", `{"model":"gpt-test","messages":[]}`},
		{a, "X-Api-Key", "fixture-provider", `{"model":"claude-test","messages":[]}`},
		{g, "X-Goog-Api-Key", "fixture-provider", `{"model":"gemini-test","contents":[]}`},
		{z, "Api-Key", "fixture-provider", `{"model":"deployment","messages":[]}`},
		{b, "Authorization", "AWS4-HMAC-SHA256", `{"model":"example","modelId":"example","messages":[]}`},
	}
	for _, tt := range providers {
		for _, path := range []string{"proxy", "router", "stream"} {
			t.Run(tt.provider.Name()+"/"+path, func(t *testing.T) {
				transport := &headerCaptureTransport{}
				client := &http.Client{Transport: transport}
				req := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
				for _, key := range []string{
					"Authorization", "Proxy-Authorization", "Cookie", "Cookie2",
					"X-Api-Key", "Api-Key", "X-Goog-Api-Key", "X-Amz-Security-Token",
					"X-Unknown-Secret", "X-Forwarded-Access-Token",
					"Sec-Websocket-Protocol", "Sec-Websocket-Key",
				} {
					req.Header.Set(key, "fixture-caller-secret")
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json")
				req.Header.Set("Connection", "keep-alive, X-Request-ID")
				req.Header.Set("X-Request-ID", "remove")
				req.Header.Set("Keep-Alive", "timeout=5")
				req.Header.Set("Anthropic-Beta", "fixture-option")
				before := req.Header.Clone()
				var err error
				if path == "proxy" {
					p := llmproxy.NewProxy(tt.provider, llmproxy.WithHTTPClient(client))
					var resp *http.Response
					resp, _, err = p.Forward(context.Background(), req)
					if resp != nil {
						resp.Body.Close()
					}
				} else {
					router := llmproxy.NewAutoRouter(
						llmproxy.WithAutoRouterHTTPClient(client),
						llmproxy.WithAutoRouterDetector(llmproxy.ProviderDetectorFunc(func(llmproxy.ProviderHint) string {
							return tt.provider.Name()
						})),
					)
					router.RegisterProvider(tt.provider)
					if path == "stream" {
						_, err = router.ForwardStreaming(context.Background(), req, httptest.NewRecorder())
					} else {
						var resp *http.Response
						resp, _, err = router.Forward(context.Background(), req)
						if resp != nil {
							resp.Body.Close()
						}
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if transport.header == nil {
					t.Fatal("outbound transport was not called")
				}
				for key, values := range transport.header {
					for _, value := range values {
						if strings.Contains(value, "fixture-caller-secret") {
							t.Errorf("caller credential forwarded in %s", key)
						}
					}
				}
				if !strings.HasPrefix(transport.header.Get(tt.authName), tt.auth) {
					t.Errorf("selected provider authentication missing from %s", tt.authName)
				}
				if tt.provider.Name() == "bedrock" && transport.header.Get("X-Amz-Security-Token") != "fixture-session" {
					t.Error("configured provider session token missing")
				}
				for _, name := range []string{"Connection", "Keep-Alive", "X-Request-ID"} {
					if transport.header.Get(name) != "" {
						t.Errorf("hop-by-hop header %s forwarded", name)
					}
				}
				if transport.header.Get("Content-Type") != "application/json" {
					t.Error("content type was not preserved")
				}
				if got := transport.header.Get("Anthropic-Beta"); (got != "") != (tt.provider.Name() == "anthropic") {
					t.Error("provider option crossed its provider boundary")
				}
				if !reflect.DeepEqual(req.Header, before) {
					t.Error("caller headers were modified")
				}
			})
		}
	}
}
