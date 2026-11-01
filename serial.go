package terminalfa

import (
	"context"
	"time"

	"github.com/dronm/goterminalfa/internal/transport"
	"github.com/sirupsen/logrus"
)

// SerialOptions configure a USB CDC ACM or other serial connection, always 8N1.
type SerialOptions struct {
	Port            string
	BaudRate        int
	ResponseTimeout time.Duration
}

// NewSerialClient creates a client without opening the port. A standalone Call
// opens and closes one connection; WithSession keeps one port for its callback.
func NewSerialClient(options SerialOptions, logger *logrus.Entry) (*Client, error) {
	exchanger, err := transport.NewSerial(transport.SerialOptions{
		Port: options.Port, BaudRate: options.BaudRate, ResponseTimeout: options.ResponseTimeout,
	}, logger)
	if err != nil {
		return nil, err
	}
	return NewClient(&serialExchanger{Serial: exchanger}), nil
}

// This adapter bridges the identical exchange interfaces without exposing the
// internal transport package in the public session API.
type serialExchanger struct{ *transport.Serial }

func (s *serialExchanger) WithSession(ctx context.Context, callback func(Exchanger) error) error {
	return s.Serial.WithSession(ctx, func(session transport.Exchanger) error {
		return callback(session)
	})
}
