package httpcache

import (
	"net/http"
	"strings"
	"time"
)

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       []byte
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	rw.body = append(rw.body, data...)
	return len(data), nil
}

// Body returns the captured response body.
func (rw *responseWriter) Body() []byte {
	return rw.body
}

// StatusCode returns the captured status code.
func (rw *responseWriter) StatusCode() int {
	if rw.statusCode == 0 {
		return http.StatusOK
	}
	return rw.statusCode
}

func (rw *responseWriter) Send() {
	rw.ResponseWriter.WriteHeader(rw.StatusCode())
	// RFC 9110 15.4.5: 304 responses MUST NOT contain a body.
	if rw.StatusCode() == http.StatusNotModified {
		return
	}
	_, _ = rw.ResponseWriter.Write(rw.body)
}

func (rw *responseWriter) ToCacheValue(r *http.Request) *CacheValue {
	// Normalize header keys to lowercase to avoid case-sensitivity issues
	// in the cached map (e.g., "ETag" vs "Etag" as separate keys).
	headers := make(map[string][]string)
	for k, v := range rw.Header() {
		headers[strings.ToLower(k)] = v
	}

	// Snapshot the request header values named by the response Vary header so
	// matchVary can compare them on future cache lookups (Vary lists request
	// header names, not response header names).
	varyReqHdrs := make(map[string]string)
	if vary := headers["vary"]; len(vary) > 0 {
		for _, field := range strings.FieldsFunc(vary[0], func(c rune) bool { return c == ',' }) {
			field = strings.TrimSpace(strings.ToLower(field))
			if field != "" && field != "*" {
				varyReqHdrs[field] = r.Header.Get(field)
			}
		}
	}

	cv := &CacheValue{
		Header:             headers,
		Body:               rw.body,
		CreatedAt:          time.Now(),
		StatusCode:         rw.StatusCode(),
		VaryRequestHeaders: varyReqHdrs,
	}

	return cv
}

type CacheValue struct {
	Header             map[string][]string `json:"headers"`
	Body               []byte              `json:"body"`
	CreatedAt          time.Time           `json:"created_at"`
	StatusCode         int                 `json:"status_code"`
	VaryRequestHeaders map[string]string   `json:"vary_request_headers,omitempty"`
}
