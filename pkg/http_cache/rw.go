package http_cache

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

// TODO: is there a way to preserve streaming to the base response writer while being able to set headers?
func (rw *responseWriter) Write(data []byte) (int, error) {
	rw.body = append(rw.body, data...)
	// return rw.ResponseWriter.Write(data)
	return 0, nil
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
}

func (rw *responseWriter) ToCacheValue() *CacheValue {
	return &CacheValue{
		Header:    rw.Header(),
		Body:      rw.body,
		CreatedAt: time.Now(),
	}
}

type CacheValue struct {
	Header    map[string][]string `json:"headers"`
	Body      []byte              `json:"body"`
	CreatedAt time.Time           `json:"created_at"`
}
