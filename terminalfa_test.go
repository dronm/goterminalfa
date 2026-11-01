package terminalfa

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type exchangeFunc func(context.Context, byte, []byte) ([]byte, error)

func (f exchangeFunc) Exchange(ctx context.Context, cmd byte, payload []byte) ([]byte, error) {
	return f(ctx, cmd, payload)
}

func TestSerialNumberCommandAndValidation(t *testing.T) {
	for _, value := range []string{"550101046182", "314", "55010104618X"} {
		t.Run(value, func(t *testing.T) {
			client := NewClient(exchangeFunc(func(_ context.Context, cmd byte, payload []byte) ([]byte, error) {
				if cmd != 0x02 || len(payload) != 0 {
					t.Fatal("incorrect wire command")
				}
				return []byte(value), nil
			}))
			got, err := client.SerialNumber(context.Background())
			if value == "550101046182" {
				if err != nil || got != value {
					t.Fatalf("serial %q, error %v", got, err)
				}
			} else {
				var invalid *InvalidSerialError
				if !errors.As(err, &invalid) {
					t.Fatalf("expected typed serial error, got %v", err)
				}
			}
		})
	}
}

func TestCanceledClientDoesNotSendCommand(t *testing.T) {
	client := NewClient(exchangeFunc(func(context.Context, byte, []byte) ([]byte, error) {
		t.Fatal("canceled request reached the device")
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.SerialNumber(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestCallAndSessionHoldOneDeviceAcrossCompleteOperation(t *testing.T) {
	var mutex sync.Mutex
	var commands []byte
	client := NewClient(exchangeFunc(func(_ context.Context, command byte, payload []byte) ([]byte, error) {
		mutex.Lock()
		commands = append(commands, command)
		mutex.Unlock()
		return payload, nil
	}))
	inside := make(chan struct{})
	resume := make(chan struct{})
	finished := make(chan error, 1)
	var escaped *Client
	go func() {
		finished <- client.WithSession(context.Background(), func(session *Client) error {
			escaped = session
			if _, err := session.Call(context.Background(), 0x23, nil); err != nil {
				return err
			}
			close(inside)
			<-resume
			_, err := session.Call(context.Background(), 0x24, nil)
			return err
		})
	}()
	<-inside
	outside := make(chan error, 1)
	go func() { _, err := client.Call(context.Background(), 0x03, nil); outside <- err }()
	select {
	case err := <-outside:
		t.Fatalf("outside call escaped operation gate: %v", err)
	case <-time.After(15 * time.Millisecond):
	}
	close(resume)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-outside; err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.Call(context.Background(), 0x03, nil); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("escaped client error %v", err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if !bytes.Equal(commands, []byte{0x23, 0x24, 0x03}) {
		t.Fatalf("command order % X", commands)
	}
}

func TestSessionCallsCanNestWithoutReacquiringParentGate(t *testing.T) {
	client := NewClient(exchangeFunc(func(_ context.Context, command byte, payload []byte) ([]byte, error) {
		return []byte{command}, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := client.WithSession(ctx, func(outer *Client) error {
		return outer.WithSession(ctx, func(inner *Client) error {
			data, err := inner.Call(ctx, 0x73, nil)
			if err == nil && !bytes.Equal(data, []byte{0x73}) {
				t.Errorf("response % X", data)
			}
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionCancellationReachesScopedCalls(t *testing.T) {
	reading := make(chan struct{})
	client := NewClient(exchangeFunc(func(ctx context.Context, command byte, payload []byte) ([]byte, error) {
		close(reading)
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- client.WithSession(ctx, func(session *Client) error {
			_, err := session.Call(context.Background(), 0x03, nil)
			return err
		})
	}()
	<-reading
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scoped call ignored operation cancellation")
	}
}
