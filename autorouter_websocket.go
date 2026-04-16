package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

var ErrWebSocketNotConfigured = errors.New("websocket forwarding is not configured")

func (a *AutoRouter) ForwardWebSocket(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if a.wsUpgrader == nil || a.wsDialer == nil {
		return ErrWebSocketNotConfigured
	}

	clientConn, err := a.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return fmt.Errorf("upgrade websocket: %w", err)
	}

	firstType, firstData, err := clientConn.ReadMessage()
	if err != nil {
		_ = clientConn.Close()
		return fmt.Errorf("read first websocket message: %w", err)
	}

	firstMsg, err := ParseWSMessage(firstData)
	if err != nil {
		_ = clientConn.Close()
		return fmt.Errorf("parse first websocket message: %w", err)
	}
	if firstMsg == nil || firstMsg.Type != "response.create" {
		_ = clientConn.Close()
		return errors.New("first websocket message must be type=response.create")
	}

	model := firstMsg.Model
	hint := ProviderHint{Model: model, Headers: r.Header}
	providerName := a.detector.Detect(hint)
	if providerName == "" && a.modelProviderLookup != nil && model != "" {
		providerName = a.modelProviderLookup(model)
	}

	var provider Provider
	if providerName != "" {
		provider, _ = a.registry.Get(providerName)
		if provider == nil {
			_ = clientConn.Close()
			return ErrNoProvider
		}
	} else {
		provider = a.fallbackProvider
		if provider == nil {
			_ = clientConn.Close()
			return ErrNoProvider
		}
		providerName = provider.Name()
	}

	wsProvider, ok := provider.(WebSocketCapableProvider)
	if !ok {
		_ = clientConn.Close()
		return fmt.Errorf("provider %q does not support websocket mode", provider.Name())
	}

	firstOut := firstData
	if strippedModel, hasPrefix := stripProviderPrefix(model); hasPrefix {
		firstOut, err = rewriteWSCreateModel(firstData, strippedModel)
		if err != nil {
			_ = clientConn.Close()
			return fmt.Errorf("rewrite first websocket message: %w", err)
		}
		model = strippedModel
	}

	meta, _, err := provider.BodyParser().Parse(io.NopCloser(bytes.NewReader(firstOut)))
	if err != nil {
		_ = clientConn.Close()
		return fmt.Errorf("parse websocket metadata: %w", err)
	}
	if meta.Custom == nil {
		meta.Custom = make(map[string]any)
	}
	meta.Custom["api_type"] = APITypeResponses
	meta.Custom["provider"] = providerName
	if meta.Model == "" {
		meta.Model = model
	}

	upstreamURL, err := wsProvider.WebSocketURL(meta)
	if err != nil {
		_ = clientConn.Close()
		return fmt.Errorf("resolve websocket url: %w", err)
	}

	headers := cloneHeader(r.Header)
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL.String(), bytes.NewReader(firstOut))
	if err != nil {
		_ = clientConn.Close()
		return fmt.Errorf("create websocket upstream request: %w", err)
	}
	upstreamReq.Header = headers
	if err := provider.RequestEnricher().Enrich(upstreamReq, meta, firstOut); err != nil {
		_ = clientConn.Close()
		return fmt.Errorf("enrich websocket request: %w", err)
	}

	upstreamConn, _, err := a.wsDialer.DialContext(ctx, upstreamURL.String(), upstreamReq.Header)
	if err != nil {
		_ = clientConn.Close()
		return fmt.Errorf("dial upstream websocket: %w", err)
	}

	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = clientConn.Close()
			_ = upstreamConn.Close()
		})
	}
	defer closeBoth()

	if err := upstreamConn.WriteMessage(firstType, firstOut); err != nil {
		return fmt.Errorf("forward first websocket message: %w", err)
	}

	var modelMu sync.RWMutex
	currentModel := model
	setModel := func(m string) {
		if m == "" {
			return
		}
		modelMu.Lock()
		currentModel = m
		meta.Model = m
		modelMu.Unlock()
	}
	getModel := func() string {
		modelMu.RLock()
		defer modelMu.RUnlock()
		return currentModel
	}

	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if relayErr := relayClientToUpstream(clientConn, upstreamConn, setModel); relayErr != nil {
			errCh <- relayErr
		}
		closeBoth()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if relayErr := a.relayUpstreamToClient(upstreamConn, clientConn, providerName, getModel); relayErr != nil {
			errCh <- relayErr
		}
		closeBoth()
	}()

	wg.Wait()
	close(errCh)

	for relayErr := range errCh {
		if relayErr != nil && !isWSRelayCloseError(relayErr) {
			return relayErr
		}
	}

	return nil
}

func relayClientToUpstream(clientConn, upstreamConn WSConn, setModel func(string)) error {
	for {
		messageType, data, err := clientConn.ReadMessage()
		if err != nil {
			return err
		}

		outData := data
		if messageType == TextMessage {
			msg, parseErr := ParseWSMessage(data)
			if parseErr == nil && msg != nil && msg.Type == "response.create" {
				model := msg.Model
				if strippedModel, hasPrefix := stripProviderPrefix(model); hasPrefix {
					var rewriteErr error
					outData, rewriteErr = rewriteWSCreateModel(data, strippedModel)
					if rewriteErr != nil {
						return rewriteErr
					}
					model = strippedModel
				}
				setModel(model)
			}
		}

		if err := upstreamConn.WriteMessage(messageType, outData); err != nil {
			return err
		}
	}
}

func (a *AutoRouter) relayUpstreamToClient(upstreamConn, clientConn WSConn, providerName string, model func() string) error {
	turn := 0
	for {
		messageType, data, err := upstreamConn.ReadMessage()
		if err != nil {
			return err
		}

		if messageType == TextMessage {
			msg, parseErr := ParseWSMessage(data)
			if parseErr == nil && msg != nil && msg.Type == "response.completed" {
				usage, usageErr := ExtractWSUsage(data)
				if usageErr != nil {
					return usageErr
				}
				if usage != nil {
					turn++
					respMeta := ResponseMetadata{
						Model: model(),
						Usage: Usage{
							PromptTokens:     usage.PromptTokens,
							CompletionTokens: usage.CompletionTokens,
							TotalTokens:      usage.TotalTokens,
						},
						Custom: map[string]any{},
					}
					if usage.CacheUsage != nil {
						respMeta.Custom["cache_usage"] = *usage.CacheUsage
					}
					if usage.ReasoningTokens > 0 {
						respMeta.Custom["reasoning_tokens"] = usage.ReasoningTokens
					}

					var billing *BillingResult
					if a.billingCalculator != nil {
						meta := BodyMetadata{Model: model(), Custom: map[string]any{"provider": providerName}}
						billing = a.billingCalculator.Calculate(meta, &respMeta)
					}
					if a.wsBillingCallback != nil {
						a.wsBillingCallback(turn, respMeta, billing)
					}
				}
			}
		}

		if err := clientConn.WriteMessage(messageType, data); err != nil {
			return err
		}
	}
}

func rewriteWSCreateModel(data []byte, model string) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	raw["model"] = model
	return json.Marshal(raw)
}

func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vv := range h {
		out[k] = append([]string(nil), vv...)
	}
	return out
}

func isWSRelayCloseError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "closed") || strings.Contains(errText, "websocket: close")
}
