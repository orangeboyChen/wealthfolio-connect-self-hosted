package snaptrade

import "fmt"

// APIError represents a non-successful, non-retriable SnapTrade response.
type APIError struct {
	StatusCode int
	Endpoint   string
	Detail     string
}

func (e *APIError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("snaptrade: %s returned HTTP %d", e.Endpoint, e.StatusCode)
	}
	return fmt.Sprintf("snaptrade: %s returned HTTP %d: %s", e.Endpoint, e.StatusCode, e.Detail)
}
