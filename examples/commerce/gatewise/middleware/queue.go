package middleware

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// Consuming a small known-length body lets net/http observe an HTTP/1.1 peer
// closing while admission waits. Bound upload time with http.Server.ReadTimeout.
// Unknown/large/streaming bodies retain normal streaming and fail-fast admission.
func (s *Service) prepareQueueBody(w http.ResponseWriter, r *http.Request) (bool, error) {
	if !s.QueueEnabled() {
		return false, nil
	}
	if r.Body == nil || r.Body == http.NoBody {
		w.Header().Set("X-Gatewise-Queue-Eligible", "true")
		return false, nil
	}
	if s.queueBodyMaxBytes < 0 || r.ContentLength < 0 || r.ContentLength > s.queueBodyMaxBytes {
		w.Header().Set("X-Gatewise-Queue-Eligible", "false")
		return true, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, s.queueBodyMaxBytes+1))
	if err != nil {
		return true, err
	}
	if int64(len(body)) > s.queueBodyMaxBytes {
		return true, fmt.Errorf("request body exceeds queue buffer")
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	w.Header().Set("X-Gatewise-Queue-Eligible", "true")
	return false, nil
}
