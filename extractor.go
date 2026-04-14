package llmproxy

import (
	"io"
	"net/http"
)

type ResponseExtractor interface {
	Extract(resp *http.Response) (metadata ResponseMetadata, rawBody []byte, err error)
}

type StreamingResponseExtractor interface {
	ResponseExtractor
	ExtractStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error)
	IsStreamingResponse(resp *http.Response) bool
}

type StreamingHandler interface {
	HandleStream(resp *http.Response, w http.ResponseWriter, meta BodyMetadata) (ResponseMetadata, error)
}

type DefaultStreamingHandler struct {
	extractor StreamingResponseExtractor
}

func NewDefaultStreamingHandler(extractor StreamingResponseExtractor) *DefaultStreamingHandler {
	return &DefaultStreamingHandler{extractor: extractor}
}

func (h *DefaultStreamingHandler) HandleStream(resp *http.Response, w http.ResponseWriter, meta BodyMetadata) (ResponseMetadata, error) {
	rc := http.NewResponseController(w)
	return h.extractor.ExtractStreamingWithController(resp, w, rc)
}

type TeeReader struct {
	r io.Reader
	w io.Writer
}

func NewTeeReader(r io.Reader, w io.Writer) *TeeReader {
	return &TeeReader{r: r, w: w}
}

func (t *TeeReader) Read(p []byte) (n int, err error) {
	n, err = t.r.Read(p)
	if n > 0 {
		if _, writeErr := t.w.Write(p[:n]); writeErr != nil {
			return n, writeErr
		}
	}
	return
}
