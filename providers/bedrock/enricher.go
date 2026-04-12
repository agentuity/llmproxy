package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/agentuity/llmproxy"
)

// Enricher implements llmproxy.RequestEnricher for AWS Bedrock.
// It handles AWS Signature V4 authentication.
type Enricher struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
	Service         string
}

// Enrich adds AWS Signature V4 headers to the request.
func (e *Enricher) Enrich(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) error {
	req.Header.Set("Content-Type", "application/json")

	// Add session token if present (for temporary credentials)
	if e.SessionToken != "" {
		req.Header.Set("x-amz-security-token", e.SessionToken)
	}

	// Sign the request with AWS Signature V4
	e.signRequest(req, rawBody)

	return nil
}

func (e *Enricher) signRequest(req *http.Request, body []byte) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	// Set required headers
	req.Header.Set("X-Amz-Date", amzDate)
	if e.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", e.SessionToken)
	}

	// Create canonical request
	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	canonicalQueryString := req.URL.Query().Encode()
	canonicalHeaders := e.buildCanonicalHeaders(req)
	signedHeaders := e.buildSignedHeaders(req)

	// Hash the payload
	payloadHash := hashSHA256(body)

	// Build canonical request
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		"",
		signedHeaders,
		payloadHash,
	}, "\n")

	// Build string to sign
	algorithm := "AWS4-HMAC-SHA256"
	credentialScope := fmt.Sprintf("%s/%s/aws4_request", dateStamp, e.Region)
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		credentialScope,
		hashSHA256([]byte(canonicalRequest)),
	}, "\n")

	// Calculate signature
	signingKey := getSignatureKey(e.SecretAccessKey, dateStamp, e.Region, e.Service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	// Build authorization header
	authHeader := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm,
		e.AccessKeyID,
		credentialScope,
		signedHeaders,
		signature,
	)

	req.Header.Set("Authorization", authHeader)
}

func (e *Enricher) buildCanonicalHeaders(req *http.Request) string {
	headers := make([]string, 0)
	for k, v := range req.Header {
		lowerKey := strings.ToLower(k)
		if lowerKey == "authorization" {
			continue
		}
		headers = append(headers, fmt.Sprintf("%s:%s", lowerKey, strings.Join(v, ",")))
	}
	sort.Strings(headers)
	return strings.Join(headers, "\n")
}

func (e *Enricher) buildSignedHeaders(req *http.Request) string {
	headers := make([]string, 0)
	for k := range req.Header {
		lowerKey := strings.ToLower(k)
		if lowerKey == "authorization" {
			continue
		}
		headers = append(headers, lowerKey)
	}
	sort.Strings(headers)
	return strings.Join(headers, ";")
}

func hashSHA256(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func getSignatureKey(key, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+key), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	return kSigning
}

// NewEnricher creates a new Bedrock enricher with AWS credentials.
//
// Parameters:
//   - region: AWS region (e.g., "us-east-1", "us-west-2")
//   - accessKeyID: AWS Access Key ID
//   - secretAccessKey: AWS Secret Access Key
//   - sessionToken: AWS Session Token (optional, for temporary credentials)
func NewEnricher(region, accessKeyID, secretAccessKey, sessionToken string) *Enricher {
	return &Enricher{
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:    sessionToken,
		Region:          region,
		Service:         "bedrock",
	}
}

// NewEnricherWithService creates an enricher with a custom service name.
// Use "bedrock-runtime" for the runtime API.
func NewEnricherWithService(region, accessKeyID, secretAccessKey, sessionToken, service string) *Enricher {
	return &Enricher{
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:    sessionToken,
		Region:          region,
		Service:         service,
	}
}
