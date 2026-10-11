package httpcache

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// responseWriter buffers an upstream response so it can be stored. A body
// larger than maxBody is not stored: what was buffered is sent and the rest
// streams straight through.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       []byte
	maxBody    int64
	streaming  bool
	// streamStatus is the cache-status sent with a streamed response.
	streamStatus string
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	size, err := strconv.ParseInt(rw.Header().Get("content-length"), 10, 64)
	if err == nil && rw.tooLarge(size) {
		_ = rw.stream()
	}
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	if !rw.streaming && rw.tooLarge(int64(len(rw.body)+len(data))) {
		if err := rw.stream(); err != nil {
			return 0, err
		}
	}
	if rw.streaming {
		return rw.ResponseWriter.Write(data)
	}
	rw.body = append(rw.body, data...)
	return len(data), nil
}

// Flush passes through once the response is streaming. Flushing a buffered
// response would send its headers before it is known whether it is stored.
func (rw *responseWriter) Flush() {
	if rw.streaming {
		_ = http.NewResponseController(rw.ResponseWriter).Flush()
	}
}

func (rw *responseWriter) tooLarge(size int64) bool {
	return rw.maxBody > 0 && size > rw.maxBody
}

func (rw *responseWriter) stream() error {
	if rw.streaming {
		return nil
	}
	rw.streaming = true
	rw.Header().Set("cache-status", rw.streamStatus)
	rw.ResponseWriter.WriteHeader(rw.StatusCode())
	body := rw.body
	rw.body = nil
	if len(body) == 0 {
		return nil
	}
	_, err := rw.ResponseWriter.Write(body)
	return err
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
	Body               []byte              `json:"-"`
	CreatedAt          time.Time           `json:"created_at"`
	StatusCode         int                 `json:"status_code"`
	VaryRequestHeaders map[string]string   `json:"vary_request_headers,omitempty"`
}

// encode stores the body after the JSON metadata so a cache hit decodes only
// the metadata.
func (cv *CacheValue) encode() []byte {
	meta, _ := json.Marshal(cv)
	out := make([]byte, 0, binary.MaxVarintLen64+len(meta)+len(cv.Body))
	out = binary.AppendUvarint(out, uint64(len(meta)))
	out = append(out, meta...)
	return append(out, cv.Body...)
}

// decodeCacheValue reads an entry written by encode. The body shares memory
// with data, which the cache never modifies.
func decodeCacheValue(data []byte) (*CacheValue, error) {
	n, k := binary.Uvarint(data)
	if k <= 0 || n > uint64(len(data)-k) {
		return nil, errors.New("corrupt cache entry")
	}
	cv := &CacheValue{}
	if err := json.Unmarshal(data[k:k+int(n)], cv); err != nil {
		return nil, err
	}
	cv.Body = data[k+int(n):]
	return cv, nil
}
