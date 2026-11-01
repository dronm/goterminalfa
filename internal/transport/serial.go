// Package transport contains transport implementations for the KKT protocol.
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/sirupsen/logrus"
	"go.bug.st/serial"
)

// Exchanger is independent of the physical connection. Serial and TCP use
// the same interface without changing command handlers.
type Exchanger interface {
	Exchange(context.Context, byte, []byte) ([]byte, error)
}

type SerialOptions struct {
	Port            string
	BaudRate        int
	ResponseTimeout time.Duration
}

// serialPort deliberately exposes only the operations this transport needs.
type serialPort interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	SetReadTimeout(time.Duration) error
	Close() error
}

type serialOpener func(string, *serial.Mode) (serialPort, error)

// Serial opens and closes a connection per standalone Exchange. WithSession
// keeps one port open across a complete operation. Fixed framing is always 8N1.
type Serial struct {
	options SerialOptions
	logger  *logrus.Entry
	open    serialOpener
}

func NewSerial(options SerialOptions, logger *logrus.Entry) (*Serial, error) {
	if strings.TrimSpace(options.Port) == "" {
		return nil, errors.New("serial port is required")
	}
	if options.BaudRate <= 0 {
		return nil, errors.New("serial baud rate must be positive")
	}
	if options.ResponseTimeout <= 0 {
		return nil, errors.New("serial response timeout must be positive")
	}
	if logger == nil {
		logger = logrus.NewEntry(logrus.New())
	}
	return &Serial{
		options: options,
		logger:  logger.WithFields(logrus.Fields{"transport": "serial", "port": options.Port}),
		open: func(name string, mode *serial.Mode) (serialPort, error) {
			return serial.Open(name, mode)
		},
	}, nil
}

// Exchange performs a single command with a fresh serial connection.
func (s *Serial) Exchange(ctx context.Context, command byte, payload []byte) (data []byte, err error) {
	if _, err := protocol.EncodeCommand(command, payload); err != nil {
		return nil, err
	}
	err = s.WithSession(ctx, func(session Exchanger) error {
		data, err = session.Exchange(ctx, command, payload)
		return err
	})
	return data, err
}

// WithSession opens one physical port for the complete callback. Callbacks must
// return after their exchanges finish. A session is unusable after an exchange
// error or after the callback returns; this prevents continuing on a desynchronized
// byte stream. The caller must own the device for the complete operation.
func (s *Serial) WithSession(ctx context.Context, callback func(Exchanger) error) error {
	if callback == nil {
		return errors.New("serial session callback is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	port, err := s.open(s.options.Port, &serial.Mode{
		BaudRate: s.options.BaudRate,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return fmt.Errorf("open serial port: %w", err)
	}
	session := &serialSession{owner: s, port: port, ctx: ctx, gate: make(chan struct{}, 1)}
	defer session.finish()
	if err := ctx.Err(); err != nil {
		return err
	}
	err = callback(session)
	if err == nil {
		err = ctx.Err()
	}
	return err
}

var errSessionClosed = errors.New("serial session is closed")
var errSessionFailed = errors.New("serial session cannot continue after a failed exchange")

type serialSession struct {
	owner     *Serial
	port      serialPort
	ctx       context.Context
	gate      chan struct{}
	closed    bool
	failed    bool
	closeOnce sync.Once
	closeErr  error
}

func (s *serialSession) closePort() {
	s.closeOnce.Do(func() { s.closeErr = s.port.Close() })
}

func (s *serialSession) finish() {
	// Join any exchange already in progress before closing. A close must never
	// race a raw-descriptor Write, whose FD could otherwise be reused by the OS.
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	s.closed = true
	s.closePort()
	if s.closeErr != nil {
		s.owner.logger.WithError(s.closeErr).Warn("close serial port failed")
	}
}

func (s *serialSession) Exchange(ctx context.Context, command byte, payload []byte) (data []byte, err error) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	if s.closed {
		return nil, errSessionClosed
	}
	if s.failed {
		return nil, errSessionFailed
	}
	defer func() {
		if err != nil {
			s.failed = true
		}
	}()
	frame, err := protocol.EncodeCommand(command, payload)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.owner.options.ResponseTimeout)
	defer cancel()
	stopParent := context.AfterFunc(s.ctx, cancel)
	defer stopParent()
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Per-command timeout is bounded by both the operation's context and its
	// caller context. Cancellation is also propagated while the session is idle.
	readTimeout := s.owner.options.ResponseTimeout
	if deadline, ok := ctx.Deadline(); ok {
		readTimeout = time.Until(deadline)
	}
	if parentDeadline, ok := s.ctx.Deadline(); ok && time.Until(parentDeadline) < readTimeout {
		readTimeout = time.Until(parentDeadline)
	}
	if readTimeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	if err := s.port.SetReadTimeout(readTimeout); err != nil {
		return nil, fmt.Errorf("set serial read timeout: %w", err)
	}
	if s.owner.logger.Logger.IsLevelEnabled(logrus.DebugLevel) {
		s.owner.logger.WithField("tx", fmt.Sprintf("% X", frame)).Debug("serial command frame")
	}
	// No cancellation-close watcher runs concurrently with synchronous Write.
	// The library writes through raw FDs. Its Open/configure/Write calls cannot
	// be forcibly interrupted; cancellation is checked before every short write.
	for remaining := frame; len(remaining) > 0; {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := s.port.Write(remaining)
		if err != nil {
			return nil, fmt.Errorf("write serial request: %w", err)
		}
		if n <= 0 || n > len(remaining) {
			return nil, fmt.Errorf("write serial request: %w", io.ErrShortWrite)
		}
		remaining = remaining[n:]
	}

	// Close is documented to unblock Read. Join the watcher before the exchange
	// ends, so no cancellation close can escape into a subsequent command Write.
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	readCanceled := false
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			readCanceled = true
			s.closePort()
		case <-done:
		}
	}()
	defer func() {
		close(done)
		<-watcherDone
		// A response can finish at the same instant as cancellation. A watcher
		// that closed the port must never leave this session reusable.
		if readCanceled && err == nil {
			data = nil
			if s.ctx.Err() != nil {
				err = s.ctx.Err()
			} else {
				err = ctx.Err()
			}
		}
	}()

	reader := &responseReader{ctx: ctx, port: s.port}
	data, err = protocol.ReadResponse(reader)
	if s.owner.logger.Logger.IsLevelEnabled(logrus.DebugLevel) {
		s.owner.logger.WithField("rx", fmt.Sprintf("% X", reader.received)).Debug("serial response frame")
	}
	if err != nil {
		if s.ctx.Err() != nil {
			return nil, fmt.Errorf("serial exchange: %w", s.ctx.Err())
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("serial exchange: %w", ctx.Err())
		}
		return nil, fmt.Errorf("serial exchange: %w", err)
	}
	return data, nil
}

type responseReader struct {
	ctx      context.Context
	port     serialPort
	received []byte
}

func (r *responseReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.port.Read(buffer)
	if n > 0 {
		r.received = append(r.received, buffer[:n]...)
	}
	if err != nil {
		if r.ctx.Err() != nil {
			return n, r.ctx.Err()
		}
		return n, err
	}
	if n == 0 {
		// go.bug.st/serial reports a timed-out read as (0, nil). Translate it
		// to an error; io.ReadFull otherwise retries forever with no progress.
		if r.ctx.Err() != nil {
			return 0, r.ctx.Err()
		}
		return 0, context.DeadlineExceeded
	}
	return n, nil
}
