package k8s

import (
	"errors"
	"fmt"
	"net/http"
)

// --------------------------------------------------------------------------
// StatusError
// --------------------------------------------------------------------------

// StatusError represents a non-2xx response from the Kubernetes API server.
// It allows callers to distinguish specific HTTP codes (e.g. 404, 409).
type StatusError struct {
	Code int
	Path string
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: status %d: %s", e.Path, e.Code, e.Body)
}

// IsNotFound returns true when err is a 404 [StatusError].
func IsNotFound(err error) bool {
	se, ok := errors.AsType[*StatusError](err)
	return ok && se.Code == http.StatusNotFound
}

// IsConflict returns true when err is a 409 [StatusError]
// (optimistic lock failure).
func IsConflict(err error) bool {
	se, ok := errors.AsType[*StatusError](err)
	return ok && se.Code == http.StatusConflict
}
