package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/sirupsen/logrus"
)

// TCPOptions configure the device's binary command server, not an HTTP endpoint.
// ResponseTimeout covers dialing, writing and reading the complete response.
type TCPOptions struct {
	Address         string
	ConnectTimeout  time.Duration
	ResponseTimeout time.Duration
}

// TCP exchanges one command per connection, as specified by protocol 01.05.
// It intentionally has no session provider: Client.WithSession retains device
// ownership while each command still opens its own TCP connection.
type TCP struct {
	options TCPOptions
	logger  *logrus.Entry
	dial    func(context.Context, string, string) (net.Conn, error)
}

func NewTCP(options TCPOptions, logger *logrus.Entry) (*TCP, error) {
	if strings.Contains(options.Address, "://") {
		return nil, errors.New("TCP address must be host:port, not a URL")
	}
	host, port, err := net.SplitHostPort(options.Address)
	if err != nil || host == "" || strings.ContainsAny(host, "/?#@ \t\r\n\\") {
		return nil, errors.New("TCP address must contain a nonempty host and numeric port")
	}
	for _, value := range port {
		if value < '0' || value > '9' {
			return nil, errors.New("TCP port must be an integer between 1 and 65535")
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, errors.New("TCP port must be an integer between 1 and 65535")
	}
	if options.ConnectTimeout <= 0 || options.ResponseTimeout <= 0 {
		return nil, errors.New("TCP connect and response timeouts must be positive")
	}
	if logger == nil {
		logger = logrus.NewEntry(logrus.New())
	}
	dialer := &net.Dialer{Timeout: options.ConnectTimeout}
	return &TCP{
		options: options,
		logger:  logger.WithFields(logrus.Fields{"transport": "tcp", "address": options.Address}),
		dial:    dialer.DialContext,
	}, nil
}

func (t *TCP) Exchange(parent context.Context, command byte, payload []byte) (data []byte, err error) {
	frame, err := protocol.EncodeCommand(command, payload)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, t.options.ResponseTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, &NotSentError{Err: err}
	}
	conn, err := t.dial(ctx, "tcp", t.options.Address)
	if err != nil {
		return nil, &NotSentError{Err: tcpError(ctx, "connect TCP device", err)}
	}
	if conn == nil {
		return nil, &NotSentError{Err: errors.New("TCP dialer returned a nil connection")}
	}
	var closeOnce sync.Once
	var closeErr error
	closeConnection := func() { closeOnce.Do(func() { closeErr = conn.Close() }) }
	defer func() {
		closeConnection()
		if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			t.logger.WithError(closeErr).Warn("close TCP connection failed")
		}
	}()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, &NotSentError{Err: tcpError(ctx, "set TCP deadline", err)}
	}
	if err := ctx.Err(); err != nil {
		return nil, &NotSentError{Err: err}
	}

	// net.Conn permits concurrent Close with Read/Write. Closing on cancellation
	// interrupts active network I/O, unlike raw serial-descriptor writes.
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	canceled := false
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			canceled = true
			closeConnection()
		case <-done:
		}
	}()
	defer func() {
		close(done)
		<-watcherDone
		if canceled && err == nil {
			data, err = nil, ctx.Err()
		}
	}()

	if t.logger.Logger.IsLevelEnabled(logrus.DebugLevel) {
		t.logger.WithField("tx", fmt.Sprintf("% X", frame)).Debug("TCP command frame")
	}
	written := 0
	writeFailure := func(err error) error {
		if written == 0 {
			return &NotSentError{Err: err}
		}
		return err
	}
	for remaining := frame; len(remaining) > 0; {
		if err := ctx.Err(); err != nil {
			return nil, writeFailure(err)
		}
		n, err := conn.Write(remaining)
		if n > 0 {
			written += n
		}
		if err != nil {
			return nil, writeFailure(tcpError(ctx, "write TCP request", err))
		}
		if n <= 0 || n > len(remaining) {
			return nil, writeFailure(fmt.Errorf("write TCP request: %w", io.ErrShortWrite))
		}
		remaining = remaining[n:]
	}

	reader := &tcpResponseReader{reader: conn}
	data, err = protocol.ReadResponse(reader)
	if t.logger.Logger.IsLevelEnabled(logrus.DebugLevel) {
		t.logger.WithField("rx", fmt.Sprintf("% X", reader.received)).Debug("TCP response frame")
	}
	if err != nil {
		return nil, tcpError(ctx, "read TCP response", err)
	}
	return data, nil
}

func tcpError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", operation, ctx.Err())
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return fmt.Errorf("%s: %w", operation, context.DeadlineExceeded)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

type tcpResponseReader struct {
	reader   io.Reader
	received []byte
}

func (r *tcpResponseReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.received = append(r.received, buffer[:n]...)
	}
	if n == 0 && err == nil && len(buffer) > 0 {
		err = io.ErrNoProgress
	}
	return n, err
}
