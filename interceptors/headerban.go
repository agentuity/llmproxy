package interceptors

import (
	"net/http"

	"github.com/agentuity/llmproxy"
)

type HeaderBanInterceptor struct {
	RequestHeaders  []string
	ResponseHeaders []string
}

func (i *HeaderBanInterceptor) Intercept(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	for _, h := range i.RequestHeaders {
		req.Header.Del(h)
	}

	resp, respMeta, rawRespBody, err := next(req)
	if err != nil {
		return resp, respMeta, rawRespBody, err
	}

	for _, h := range i.ResponseHeaders {
		resp.Header.Del(h)
	}

	return resp, respMeta, rawRespBody, nil
}

func NewHeaderBan(requestHeaders, responseHeaders []string) *HeaderBanInterceptor {
	return &HeaderBanInterceptor{
		RequestHeaders:  requestHeaders,
		ResponseHeaders: responseHeaders,
	}
}

func NewResponseHeaderBan(headers ...string) *HeaderBanInterceptor {
	return &HeaderBanInterceptor{
		ResponseHeaders: headers,
	}
}

func NewRequestHeaderBan(headers ...string) *HeaderBanInterceptor {
	return &HeaderBanInterceptor{
		RequestHeaders: headers,
	}
}
