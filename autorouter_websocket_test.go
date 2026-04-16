package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type wsFrame struct {
	messageType int
	data        []byte
}

type mockWSConn struct {
	incoming chan wsFrame
	peer     *mockWSConn
	closed   atomic.Bool
	closeCh  chan struct{}
}

func newMockWSLinkedPair() (*mockWSConn, *mockWSConn) {
	a := &mockWSConn{incoming: make(chan wsFrame, 32), closeCh: make(chan struct{})}
	b := &mockWSConn{incoming: make(chan wsFrame, 32), closeCh: make(chan struct{})}
	a.peer = b
	b.peer = a
	return a, b
}

func (c *mockWSConn) ReadMessage() (int, []byte, error) {
	select {
	case frame := <-c.incoming:
		return frame.messageType, append([]byte(nil), frame.data...), nil
	case <-c.closeCh:
		return 0, nil, io.EOF
	}
}

func (c *mockWSConn) WriteMessage(messageType int, data []byte) error {
	if c.closed.Load() {
		return io.EOF
	}
	if c.peer == nil || c.peer.closed.Load() {
		return io.EOF
	}
	select {
	case c.peer.incoming <- wsFrame{messageType: messageType, data: append([]byte(nil), data...)}:
		return nil
	case <-c.closeCh:
		return io.EOF
	case <-c.peer.closeCh:
		return io.EOF
	}
}

func (c *mockWSConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		close(c.closeCh)
		if c.peer != nil {
			c.peer.closeFromPeer()
		}
	}
	return nil
}

func (c *mockWSConn) closeFromPeer() {
	if c.closed.CompareAndSwap(false, true) {
		close(c.closeCh)
	}
}

type mockWSUpgrader struct {
	conn   WSConn
	err    error
	called atomic.Bool
}

func (u *mockWSUpgrader) Upgrade(w http.ResponseWriter, r *http.Request, h http.Header) (WSConn, error) {
	u.called.Store(true)
	if u.err != nil {
		return nil, u.err
	}
	return u.conn, nil
}

type mockWSDialer struct {
	conn         WSConn
	err          error
	dialedURL    string
	dialedHeader http.Header
	mu           sync.Mutex
}

func (d *mockWSDialer) DialContext(ctx context.Context, urlStr string, h http.Header) (WSConn, *http.Response, error) {
	d.mu.Lock()
	d.dialedURL = urlStr
	d.dialedHeader = cloneHeader(h)
	d.mu.Unlock()
	if d.err != nil {
		return nil, nil, d.err
	}
	return d.conn, &http.Response{StatusCode: http.StatusSwitchingProtocols}, nil
}

type mockWSProvider struct {
	*mockProvider
	wsURL *url.URL
	err   error
}

func (m *mockWSProvider) WebSocketURL(meta BodyMetadata) (*url.URL, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.wsURL, nil
}

func wsTestProvider(t *testing.T, name string, wsURL string) *mockWSProvider {
	t.Helper()
	u, err := url.Parse(wsURL)
	if err != nil {
		t.Fatalf("url parse: %v", err)
	}

	return &mockWSProvider{
		mockProvider: &mockProvider{
			name: name,
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				var raw map[string]any
				_ = json.Unmarshal(data, &raw)
				model, _ := raw["model"].(string)
				return BodyMetadata{Model: model, Custom: map[string]any{}}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error {
				if req.Header.Get("Authorization") == "" {
					req.Header.Set("Authorization", "Bearer enriched")
				}
				return nil
			},
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return u, nil
			},
			extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
				return ResponseMetadata{}, nil, nil
			},
		},
		wsURL: u,
	}
}

type wsFixture struct {
	router       *AutoRouter
	client       *mockWSConn
	upstream     *mockWSConn
	upgrader     *mockWSUpgrader
	dialer       *mockWSDialer
	providerName string
}

func newWSFixture(t *testing.T, provider Provider) *wsFixture {
	t.Helper()
	clientApp, routerClient := newMockWSLinkedPair()
	upstreamApp, routerUpstream := newMockWSLinkedPair()

	upgrader := &mockWSUpgrader{conn: routerClient}
	dialer := &mockWSDialer{conn: routerUpstream}

	router := NewAutoRouter(
		WithAutoRouterWebSocket(upgrader, dialer),
		WithAutoRouterDetector(ProviderDetectorFunc(func(h ProviderHint) string {
			if h.Model == "" {
				return "openai"
			}
			return DetectProviderFromModel(h.Model)
		})),
	)
	router.RegisterProvider(provider)

	return &wsFixture{
		router:       router,
		client:       clientApp,
		upstream:     upstreamApp,
		upgrader:     upgrader,
		dialer:       dialer,
		providerName: provider.Name(),
	}
}

func mustReadFrame(t *testing.T, c *mockWSConn) wsFrame {
	t.Helper()
	ch := make(chan wsFrame, 1)
	errCh := make(chan error, 1)
	go func() {
		mt, data, err := c.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		ch <- wsFrame{messageType: mt, data: data}
	}()
	select {
	case f := <-ch:
		return f
	case err := <-errCh:
		t.Fatalf("read frame error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for frame")
	}
	return wsFrame{}
}

func mustReadError(t *testing.T, c *mockWSConn) error {
	t.Helper()
	ch := make(chan error, 1)
	go func() {
		_, _, err := c.ReadMessage()
		ch <- err
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for read error")
	}
	return nil
}

func startForwardWS(t *testing.T, f *wsFixture, reqHeaders http.Header) chan error {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/v1/responses", nil)
	for k, vv := range reqHeaders {
		req.Header[k] = vv
	}
	w := httptest.NewRecorder()
	errCh := make(chan error, 1)
	go func() {
		errCh <- f.router.ForwardWebSocket(context.Background(), w, req)
	}()
	return errCh
}

func TestForwardWebSocket_BasicRelay(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)

	errCh := startForwardWS(t, f, http.Header{"Authorization": []string{"Bearer test"}})

	if err := f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)); err != nil {
		t.Fatalf("client write: %v", err)
	}

	first := mustReadFrame(t, f.upstream)
	if first.messageType != TextMessage {
		t.Fatalf("upstream messageType=%d, want TextMessage", first.messageType)
	}

	_ = f.upstream.WriteMessage(TextMessage, []byte(`{"type":"response.created","response":{"id":"resp_1"}}`))
	_ = f.upstream.WriteMessage(TextMessage, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`))

	created := mustReadFrame(t, f.client)
	completed := mustReadFrame(t, f.client)
	if !bytes.Contains(created.data, []byte(`"response.created"`)) {
		t.Fatalf("expected response.created frame, got %s", string(created.data))
	}
	if !bytes.Contains(completed.data, []byte(`"response.completed"`)) {
		t.Fatalf("expected response.completed frame, got %s", string(completed.data))
	}

	_ = f.client.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("ForwardWebSocket() error = %v", err)
	}
}

func TestForwardWebSocket_UsageExtraction(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)

	var gotMeta ResponseMetadata
	f.router.wsBillingCallback = func(turn int, meta ResponseMetadata, billing *BillingResult) { gotMeta = meta }

	errCh := startForwardWS(t, f, http.Header{})
	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)

	_ = f.upstream.WriteMessage(TextMessage, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":22,"total_tokens":33}}}`))
	_ = mustReadFrame(t, f.client)
	_ = f.client.Close()
	_ = <-errCh

	if gotMeta.Usage.PromptTokens != 11 || gotMeta.Usage.CompletionTokens != 22 || gotMeta.Usage.TotalTokens != 33 {
		t.Fatalf("unexpected usage: %+v", gotMeta.Usage)
	}
}

func TestForwardWebSocket_CacheUsage(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)

	var gotCache int
	f.router.wsBillingCallback = func(turn int, meta ResponseMetadata, billing *BillingResult) {
		if cu, ok := meta.Custom["cache_usage"].(CacheUsage); ok {
			gotCache = cu.CachedTokens
		}
	}

	errCh := startForwardWS(t, f, http.Header{})
	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)
	_ = f.upstream.WriteMessage(TextMessage, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25,"input_tokens_details":{"cached_tokens":4}}}}`))
	_ = mustReadFrame(t, f.client)
	_ = f.client.Close()
	_ = <-errCh

	if gotCache != 4 {
		t.Fatalf("cached tokens = %d, want 4", gotCache)
	}
}

func TestForwardWebSocket_ReasoningTokens(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)

	var gotPrompt int
	var gotReasoning int
	f.router.wsBillingCallback = func(turn int, meta ResponseMetadata, billing *BillingResult) {
		gotPrompt = meta.Usage.PromptTokens
		if rt, ok := meta.Custom["reasoning_tokens"].(int); ok {
			gotReasoning = rt
		}
	}

	errCh := startForwardWS(t, f, http.Header{})
	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)
	_ = f.upstream.WriteMessage(TextMessage, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":8,"output_tokens":6,"total_tokens":14,"output_tokens_details":{"reasoning_tokens":2}}}}`))
	_ = mustReadFrame(t, f.client)
	_ = f.client.Close()
	_ = <-errCh

	if gotPrompt != 8 {
		t.Fatalf("prompt tokens = %d, want 8", gotPrompt)
	}
	if gotReasoning != 2 {
		t.Fatalf("reasoning_tokens = %d, want 2", gotReasoning)
	}
}

func TestForwardWebSocket_ModelPrefixStripping(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)
	errCh := startForwardWS(t, f, http.Header{})

	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"openai/gpt-4o","input":[]}`))
	first := mustReadFrame(t, f.upstream)
	if bytes.Contains(first.data, []byte("openai/gpt-4o")) {
		t.Fatalf("expected stripped model, got %s", string(first.data))
	}
	if !bytes.Contains(first.data, []byte(`"model":"gpt-4o"`)) {
		t.Fatalf("expected model gpt-4o, got %s", string(first.data))
	}

	_ = f.client.Close()
	_ = <-errCh
}

func TestForwardWebSocket_MultiTurn(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)
	var turns int
	f.router.wsBillingCallback = func(turn int, meta ResponseMetadata, billing *BillingResult) { turns = turn }

	errCh := startForwardWS(t, f, http.Header{})

	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)
	_ = f.upstream.WriteMessage(TextMessage, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`))
	_ = mustReadFrame(t, f.client)

	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"openai/gpt-4o","previous_response_id":"resp_1","input":[]}`))
	second := mustReadFrame(t, f.upstream)
	if bytes.Contains(second.data, []byte("openai/gpt-4o")) {
		t.Fatalf("expected stripped model in second turn")
	}
	_ = f.upstream.WriteMessage(TextMessage, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":4,"output_tokens":5,"total_tokens":9}}}`))
	_ = mustReadFrame(t, f.client)

	_ = f.client.Close()
	_ = <-errCh

	if turns != 2 {
		t.Fatalf("turns = %d, want 2", turns)
	}
}

func TestForwardWebSocket_BillingCallback(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)

	calc := NewBillingCalculator(func(provider, model string) (CostInfo, bool) {
		return CostInfo{Input: 1.0, Output: 2.0, CacheRead: 0.5}, true
	}, nil)
	f.router.billingCalculator = calc

	var gotTurns []int
	var gotCosts []float64
	f.router.wsBillingCallback = func(turn int, meta ResponseMetadata, billing *BillingResult) {
		gotTurns = append(gotTurns, turn)
		if billing != nil {
			gotCosts = append(gotCosts, billing.TotalCost)
		}
	}

	errCh := startForwardWS(t, f, http.Header{})
	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)
	_ = f.upstream.WriteMessage(TextMessage, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":1000,"output_tokens":500,"total_tokens":1500}}}`))
	_ = mustReadFrame(t, f.client)
	_ = f.client.Close()
	_ = <-errCh

	if len(gotTurns) != 1 || gotTurns[0] != 1 {
		t.Fatalf("got turns = %v, want [1]", gotTurns)
	}
	if len(gotCosts) != 1 || gotCosts[0] <= 0 {
		t.Fatalf("unexpected billing callback costs: %v", gotCosts)
	}
}

func TestForwardWebSocket_ClientClose(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)
	errCh := startForwardWS(t, f, http.Header{})
	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)

	_ = f.client.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("ForwardWebSocket() error = %v", err)
	}
	if err := mustReadError(t, f.upstream); err == nil {
		t.Fatal("expected upstream side to close")
	}
}

func TestForwardWebSocket_UpstreamClose(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)
	errCh := startForwardWS(t, f, http.Header{})
	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)

	_ = f.upstream.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("ForwardWebSocket() error = %v", err)
	}
	if err := mustReadError(t, f.client); err == nil {
		t.Fatal("expected client side to close")
	}
}

func TestForwardWebSocket_ErrorEvent(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)
	errCh := startForwardWS(t, f, http.Header{})

	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)
	_ = f.upstream.WriteMessage(TextMessage, []byte(`{"type":"response.failed","error":{"message":"boom"}}`))
	fail := mustReadFrame(t, f.client)
	if !bytes.Contains(fail.data, []byte(`"response.failed"`)) {
		t.Fatalf("expected response.failed passthrough, got %s", string(fail.data))
	}
	_ = f.client.Close()
	_ = <-errCh
}

func TestForwardWebSocket_NoWSUpgrader(t *testing.T) {
	router := NewAutoRouter()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/v1/responses", nil)
	w := httptest.NewRecorder()
	err := router.ForwardWebSocket(context.Background(), w, req)
	if !errors.Is(err, ErrWebSocketNotConfigured) {
		t.Fatalf("error = %v, want ErrWebSocketNotConfigured", err)
	}
}

func TestForwardWebSocket_NonWSProvider(t *testing.T) {
	provider := &mockProvider{
		name: "openai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4o", Custom: map[string]any{}}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse("https://api.openai.com/v1/responses")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			return ResponseMetadata{}, nil, nil
		},
	}

	f := newWSFixture(t, provider)
	errCh := startForwardWS(t, f, http.Header{})
	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	err := <-errCh
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("does not support websocket mode")) {
		t.Fatalf("expected non-websocket provider error, got %v", err)
	}
}

func TestForwardWebSocket_PassthroughNonCreateMessages(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)
	errCh := startForwardWS(t, f, http.Header{})

	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)

	original := []byte(`{"type":"response.cancel","response_id":"resp_1"}`)
	_ = f.client.WriteMessage(TextMessage, original)
	passthrough := mustReadFrame(t, f.upstream)
	if !bytes.Equal(passthrough.data, original) {
		t.Fatalf("expected byte-for-byte passthrough\n got: %s\nwant: %s", string(passthrough.data), string(original))
	}

	_ = f.client.Close()
	_ = <-errCh
}

func TestServeHTTP_WebSocketDetection(t *testing.T) {
	provider := wsTestProvider(t, "openai", "wss://api.openai.com/v1/responses")
	f := newWSFixture(t, provider)

	req := httptest.NewRequest(http.MethodGet, "http://localhost/v1/responses", nil)
	req.Header.Set("Connection", "keep-alive, upgrade")
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		f.router.ServeHTTP(w, req)
		close(done)
	}()

	_ = f.client.WriteMessage(TextMessage, []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	_ = mustReadFrame(t, f.upstream)
	_ = f.client.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeHTTP did not complete")
	}

	if !f.upgrader.called.Load() {
		t.Fatal("expected websocket upgrader to be called")
	}
}

func TestServeHTTP_NonWebSocketUnchanged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "openai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4o", Custom: map[string]any{}}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			data, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{}, data, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(h ProviderHint) string { return "openai" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4o"}`)))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got, want := w.Body.String(), `{"id":"ok"}`; got != want {
		t.Fatalf("body=%q, want %q", got, want)
	}
}
