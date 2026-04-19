package httpcache

import (
	"net/http"
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

func (rw *responseWriter) ToCacheValue() *CacheValue {
	cv := &CacheValue{
		Header:     rw.Header(),
		Body:       rw.body,
		CreatedAt:  time.Now(),
		StatusCode: rw.StatusCode(),
	}

	return cv
}

type CacheValue struct {
	Header     map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
	CreatedAt  time.Time           `json:"created_at"`
	StatusCode int                 `json:"status_code"`
}
