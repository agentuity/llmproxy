package googleai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/agentuity/llmproxy"
)

type StreamingExtractor struct {
	*Extractor
}

func NewStreamingExtractor() *StreamingExtractor {
	return &StreamingExtractor{
		Extractor: NewExtractor(),
	}
}

func (e *StreamingExtractor) IsStreamingResponse(resp *http.Response) bool {
	return llmproxy.IsSSEStream(resp.Header.Get("Content-Type"))
}

func (e *StreamingExtractor) ExtractStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	return e.extractNonStreamingWithController(resp, w, rc)
}

func (e *StreamingExtractor) extractNonStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	var buf bytes.Buffer
	tee := io.TeeReader(resp.Body, &buf)

	meta, _, err := e.Extractor.Extract(&http.Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       io.NopCloser(tee),
	})
	if err != nil {
		return meta, err
	}

	readBuf := make([]byte, 1024*512)
	for {
		n, err := buf.Read(readBuf)
		if err != nil {
			if err == io.EOF {
				if n > 0 {
					if _, writeErr := w.Write(readBuf[:n]); writeErr != nil {
						return meta, writeErr
					}
				}
				break
			}
			if errors.Is(err, context.Canceled) {
				break
			}
			return meta, err
		}
		if n == 0 {
			break
		}
		if _, writeErr := w.Write(readBuf[:n]); writeErr != nil {
			return meta, writeErr
		}
		if flushErr := rc.Flush(); flushErr != nil {
			return meta, flushErr
		}
	}

	return meta, nil
}
