package transport

import "fmt"

// NotSentError certifies that a command failed before any request bytes were
// written. It allows the service to persist a definite failure rather than an
// uncertain device outcome. Once any bytes are written, this type must not be
// used, even if the transport later reports an error.
type NotSentError struct {
	Err error
}

func (e *NotSentError) Error() string {
	return fmt.Sprintf("command was not sent: %v", e.Err)
}

func (e *NotSentError) Unwrap() error { return e.Err }
