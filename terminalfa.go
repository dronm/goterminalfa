// Package terminalfa provides a Terminal FA client independent of its transport.
package terminalfa

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

// Exchanger exchanges one complete protocol command and returns its decoded data.
// Serial and TCP implementations share this boundary.
type Exchanger interface {
	Exchange(context.Context, byte, []byte) ([]byte, error)
}

// Client serializes complete operations on one device. Separate Client instances
// must not be used to control the same physical device concurrently.
type Client struct {
	exchanger      Exchanger
	gate           chan struct{}
	closed         atomic.Bool
	sessionContext context.Context
}

func NewClient(exchanger Exchanger) *Client {
	if exchanger == nil {
		panic("terminalfa: nil exchanger")
	}
	return &Client{exchanger: exchanger, gate: make(chan struct{}, 1)}
}

// ErrSessionClosed means a callback-scoped client was used after its session ended.
var ErrSessionClosed = errors.New("terminalfa: session is closed")

type sessionProvider interface {
	WithSession(context.Context, func(Exchanger) error) error
}

// Call sends one protocol command and returns its successful response payload.
// Callers using this lower-level API must encode command parameters according to
// the protocol. HTTP applications use the validated command catalogue instead.
func (c *Client) Call(ctx context.Context, command byte, payload []byte) ([]byte, error) {
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if c.closed.Load() {
		return nil, ErrSessionClosed
	}
	ctx, cancel := combineContext(ctx, c.sessionContext)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := c.exchanger.Exchange(ctx, command, payload)
	if c.sessionContext != nil && c.sessionContext.Err() != nil && err != nil {
		return nil, c.sessionContext.Err()
	}
	return data, err
}

// WithSession retains exclusive ownership for the complete callback. A serial
// transport opens its port once for the callback; other transports can choose
// their own lifecycle. Use the supplied scoped client, not the parent client,
// inside the callback. The scoped client must not escape the callback.
func (c *Client) WithSession(ctx context.Context, callback func(*Client) error) error {
	if callback == nil {
		return errors.New("terminalfa: session callback is required")
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if c.closed.Load() {
		return ErrSessionClosed
	}
	ctx, cancel := combineContext(ctx, c.sessionContext)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	use := func(exchanger Exchanger) error {
		scoped := NewClient(exchanger)
		scoped.sessionContext = ctx
		// Join an already-running scoped Call before ending the transport session.
		// Any calls that begin after this boundary observe ErrSessionClosed.
		defer func() {
			scoped.gate <- struct{}{}
			scoped.closed.Store(true)
			<-scoped.gate
		}()
		return callback(scoped)
	}
	if provider, ok := c.exchanger.(sessionProvider); ok && c.sessionContext == nil {
		return provider.WithSession(ctx, use)
	}
	err := use(c.exchanger)
	if err == nil {
		err = ctx.Err()
	}
	return err
}

func combineContext(ctx, session context.Context) (context.Context, context.CancelFunc) {
	if session == nil {
		return context.WithCancel(ctx)
	}
	var derived context.Context
	var cancel context.CancelFunc
	if deadline, ok := session.Deadline(); ok {
		derived, cancel = context.WithDeadline(ctx, deadline)
	} else {
		derived, cancel = context.WithCancel(ctx)
	}
	stop := context.AfterFunc(session, cancel)
	if session.Err() != nil {
		cancel()
	}
	return derived, func() { stop(); cancel() }
}

// SerialNumber reads the KKT factory serial with command 0x02. It is not the USB
// descriptor serial. The protocol specifies exactly twelve decimal characters.
func (c *Client) SerialNumber(ctx context.Context) (string, error) {
	data, err := c.Call(ctx, 0x02, nil)
	if err != nil {
		return "", err
	}
	if len(data) != 12 {
		return "", &InvalidSerialError{Message: fmt.Sprintf("expected 12 bytes, received %d", len(data))}
	}
	for _, b := range data {
		if b < '0' || b > '9' {
			return "", &InvalidSerialError{Message: "factory serial contains non-decimal bytes"}
		}
	}
	return string(data), nil
}

type InvalidSerialError struct{ Message string }

func (e *InvalidSerialError) Error() string { return "invalid serial response: " + e.Message }
