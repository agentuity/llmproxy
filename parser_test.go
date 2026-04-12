package llmproxy

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// MockBodyParser is a test double for BodyParser.
type MockBodyParser struct {
	Meta    BodyMetadata
	RawBody []byte
	Err     error
}

func (m *MockBodyParser) Parse(body io.ReadCloser) (BodyMetadata, []byte, error) {
	body.Close()
	return m.Meta, m.RawBody, m.Err
}

// MockRequestEnricher is a test double for RequestEnricher.
type MockRequestEnricher struct {
	Err error
}

func (m *MockRequestEnricher) Enrich(req interface{}, meta BodyMetadata, rawBody []byte) error {
	return m.Err
}

// MockResponseExtractor is a test double for ResponseExtractor.
type MockResponseExtractor struct {
	Meta    ResponseMetadata
	RawBody []byte
	Err     error
}

func (m *MockResponseExtractor) Extract(resp interface{}) (ResponseMetadata, []byte, error) {
	return m.Meta, m.RawBody, m.Err
}

// MockURLResolver is a test double for URLResolver.
type MockURLResolver struct {
	URL string
	Err error
}

func (m *MockURLResolver) Resolve(meta BodyMetadata) (interface{}, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.URL, m.Err
}

func TestBodyParser(t *testing.T) {
	t.Run("returns metadata and raw body", func(t *testing.T) {
		expected := BodyMetadata{Model: "gpt-4"}
		raw := []byte(`{"model":"gpt-4"}`)
		parser := &MockBodyParser{Meta: expected, RawBody: raw}

		meta, body, err := parser.Parse(io.NopCloser(bytes.NewReader(raw)))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Model != "gpt-4" {
			t.Errorf("expected model gpt-4, got %s", meta.Model)
		}
		if !bytes.Equal(body, raw) {
			t.Errorf("raw body mismatch")
		}
	})

	t.Run("returns error on failure", func(t *testing.T) {
		parser := &MockBodyParser{Err: errors.New("parse error")}
		_, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte{})))
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestBaseProvider(t *testing.T) {
	t.Run("returns configured values", func(t *testing.T) {
		parser := &MockBodyParser{}
		provider := NewBaseProvider("test",
			WithBodyParser(parser),
		)

		if provider.Name() != "test" {
			t.Errorf("expected name test, got %s", provider.Name())
		}
		if provider.BodyParser() != parser {
			t.Error("body parser mismatch")
		}
	})

	t.Run("returns nil for unconfigured components", func(t *testing.T) {
		provider := NewBaseProvider("test")
		if provider.BodyParser() != nil {
			t.Error("expected nil body parser")
		}
	})
}
