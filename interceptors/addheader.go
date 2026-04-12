package interceptors

import (
	"net/http"

	"github.com/agentuity/llmproxy"
)

type Header struct {
	Key   string
	Value string
}

// NewHeader is a convenience to return a key value pair
func NewHeader(key, val string) Header {
	return Header{Key: key, Value: val}
}

type AddHeaderInterceptor struct {
	RequestHeaders  []Header
	ResponseHeaders []Header
}

func (i *AddHeaderInterceptor) Intercept(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	for _, h := range i.RequestHeaders {
		req.Header.Set(h.Key, h.Value)
	}

	resp, respMeta, rawRespBody, err := next(req)
	if err != nil {
		return resp, respMeta, rawRespBody, err
	}

	for _, h := range i.ResponseHeaders {
		resp.Header.Set(h.Key, h.Value)
	}

	return resp, respMeta, rawRespBody, nil
}

func NewAddHeader(requestHeaders, responseHeaders []Header) *AddHeaderInterceptor {
	return &AddHeaderInterceptor{
		RequestHeaders:  requestHeaders,
		ResponseHeaders: responseHeaders,
	}
}

func NewAddResponseHeader(headers ...Header) *AddHeaderInterceptor {
	return &AddHeaderInterceptor{
		ResponseHeaders: headers,
	}
}

func NewAddRequestHeader(headers ...Header) *AddHeaderInterceptor {
	return &AddHeaderInterceptor{
		RequestHeaders: headers,
	}
}
