package terminalfa

import (
	"time"

	"github.com/dronm/goterminalfa/internal/transport"
	"github.com/sirupsen/logrus"
)

// TCPOptions configure the terminal's binary protocol over TCP, not an HTTP URL.
// Address is host:port; both timeouts must be positive. ResponseTimeout bounds
// the entire exchange, including connecting, sending, and reading one frame.
type TCPOptions struct {
	Address         string
	ConnectTimeout  time.Duration
	ResponseTimeout time.Duration
}

// NewTCPClient creates a client without connecting. Every protocol command uses
// a fresh TCP connection, as required by the terminal's exchange procedure.
// WithSession retains exclusive operation ownership across these connections.
func NewTCPClient(options TCPOptions, logger *logrus.Entry) (*Client, error) {
	exchanger, err := transport.NewTCP(transport.TCPOptions{
		Address: options.Address, ConnectTimeout: options.ConnectTimeout, ResponseTimeout: options.ResponseTimeout,
	}, logger)
	if err != nil {
		return nil, err
	}
	return NewClient(exchanger), nil
}

// CommandNotSentError reports that transport failure occurred before any request
// bytes were written. It preserves its cause for errors.Is/errors.As. A failure
// after any bytes were sent cannot make this guarantee.
type CommandNotSentError = transport.NotSentError
