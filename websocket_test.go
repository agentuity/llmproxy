package llmproxy

import "testing"

func TestParseWSMessage_ResponseCreate(t *testing.T) {
	msg, err := ParseWSMessage([]byte(`{"type":"response.create","model":"gpt-4o","input":[]}`))
	if err != nil {
		t.Fatalf("ParseWSMessage() error = %v", err)
	}
	if msg.Type != "response.create" {
		t.Fatalf("Type = %q, want response.create", msg.Type)
	}
	if msg.Model != "gpt-4o" {
		t.Fatalf("Model = %q, want gpt-4o", msg.Model)
	}
}

func TestParseWSMessage_ResponseCreateWithPreviousID(t *testing.T) {
	msg, err := ParseWSMessage([]byte(`{"type":"response.create","model":"gpt-4o","previous_response_id":"resp_123"}`))
	if err != nil {
		t.Fatalf("ParseWSMessage() error = %v", err)
	}
	if msg.PreviousResponseID != "resp_123" {
		t.Fatalf("PreviousResponseID = %q, want resp_123", msg.PreviousResponseID)
	}
}

func TestParseWSMessage_NonCreate(t *testing.T) {
	msg, err := ParseWSMessage([]byte(`{"type":"response.output_text.delta","delta":"hi"}`))
	if err != nil {
		t.Fatalf("ParseWSMessage() error = %v", err)
	}
	if msg.Type != "response.output_text.delta" {
		t.Fatalf("Type = %q, want response.output_text.delta", msg.Type)
	}
}

func TestParseWSMessage_MalformedJSON(t *testing.T) {
	if _, err := ParseWSMessage([]byte(`{"type":`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestExtractWSUsage_Completed(t *testing.T) {
	usage, err := ExtractWSUsage([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}}`))
	if err != nil {
		t.Fatalf("ExtractWSUsage() error = %v", err)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 7 || usage.TotalTokens != 19 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestExtractWSUsage_CompletedWithCache(t *testing.T) {
	usage, err := ExtractWSUsage([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25,"input_tokens_details":{"cached_tokens":4}}}}`))
	if err != nil {
		t.Fatalf("ExtractWSUsage() error = %v", err)
	}
	if usage == nil || usage.CacheUsage == nil {
		t.Fatal("expected cache usage")
	}
	if usage.CacheUsage.CachedTokens != 4 {
		t.Fatalf("CachedTokens = %d, want 4", usage.CacheUsage.CachedTokens)
	}
}

func TestExtractWSUsage_CompletedWithReasoning(t *testing.T) {
	usage, err := ExtractWSUsage([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":8,"total_tokens":18,"output_tokens_details":{"reasoning_tokens":3}}}}`))
	if err != nil {
		t.Fatalf("ExtractWSUsage() error = %v", err)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 8 || usage.TotalTokens != 18 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if usage.ReasoningTokens != 3 {
		t.Fatalf("ReasoningTokens = %d, want 3", usage.ReasoningTokens)
	}
}

func TestExtractWSUsage_NonCompleted(t *testing.T) {
	usage, err := ExtractWSUsage([]byte(`{"type":"response.created","response":{"id":"resp_1"}}`))
	if err != nil {
		t.Fatalf("ExtractWSUsage() error = %v", err)
	}
	if usage != nil {
		t.Fatalf("usage = %+v, want nil", usage)
	}
}

func TestExtractWSUsage_MalformedJSON(t *testing.T) {
	if _, err := ExtractWSUsage([]byte(`{"type":`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
