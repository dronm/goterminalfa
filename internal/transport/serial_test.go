package transport

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/sirupsen/logrus"
	"go.bug.st/serial"
)

type fakePort struct {
	reader     io.Reader
	readFn     func([]byte) (int, error)
	configErr  error
	writeErr   error
	writeLimit int
	written    bytes.Buffer
	closeCalls atomic.Int32
	closed     chan struct{}
	closeOnce  sync.Once
}

func newFakePort() *fakePort {
	frame, _ := hex.DecodeString("b629000d0035353031303130343631383252de")
	return &fakePort{reader: bytes.NewReader(frame), closed: make(chan struct{})}
}

func (p *fakePort) Read(buffer []byte) (int, error) {
	if p.readFn != nil {
		return p.readFn(buffer)
	}
	return p.reader.Read(buffer)
}

func (p *fakePort) Write(buffer []byte) (int, error) {
	if p.writeErr != nil {
		return 0, p.writeErr
	}
	if p.writeLimit > 0 && len(buffer) > p.writeLimit {
		buffer = buffer[:p.writeLimit]
	}
	return p.written.Write(buffer)
}

func (p *fakePort) SetReadTimeout(time.Duration) error { return p.configErr }

func (p *fakePort) Close() error {
	p.closeCalls.Add(1)
	p.closeOnce.Do(func() { close(p.closed) })
	return nil
}

func testTransport(t *testing.T, port *fakePort) *Serial {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	transport, err := NewSerial(SerialOptions{
		Port: "/dev/test-kkt", BaudRate: 115200, ResponseTimeout: time.Second,
	}, logrus.NewEntry(logger))
	if err != nil {
		t.Fatal(err)
	}
	transport.open = func(name string, mode *serial.Mode) (serialPort, error) {
		if name != "/dev/test-kkt" || mode.BaudRate != 115200 || mode.DataBits != 8 || mode.Parity != serial.NoParity || mode.StopBits != serial.OneStopBit {
			t.Errorf("unexpected serial mode: %q %+v", name, mode)
		}
		return port, nil
	}
	return transport
}

func TestSerialExchangeHandlesShortWritesAndCloses(t *testing.T) {
	port := newFakePort()
	port.writeLimit = 2
	transport := testTransport(t, port)
	data, err := transport.Exchange(context.Background(), 0x02, nil)
	if err != nil || string(data) != "550101046182" {
		t.Fatalf("response %q, error %v", data, err)
	}
	if got := hex.EncodeToString(port.written.Bytes()); got != "b629000102efdf" {
		t.Fatalf("written packet = %s", got)
	}
	if got := port.closeCalls.Load(); got != 1 {
		t.Fatalf("close calls = %d", got)
	}
}

func TestSerialClosesAfterFailures(t *testing.T) {
	broken := errors.New("simulated failure")
	cases := []struct {
		name  string
		setup func(*fakePort)
		want  error
	}{
		{"configuration", func(p *fakePort) { p.configErr = broken }, broken},
		{"write", func(p *fakePort) { p.writeErr = broken }, broken},
		{"read", func(p *fakePort) { p.readFn = func([]byte) (int, error) { return 0, broken } }, broken},
		{"empty timed-out read", func(p *fakePort) { p.readFn = func([]byte) (int, error) { return 0, nil } }, context.DeadlineExceeded},
		{"bad CRC", func(p *fakePort) {
			frame, _ := protocol.EncodeCommand(0, []byte("550101046182"))
			frame[len(frame)-1] ^= 1
			p.reader = bytes.NewReader(frame)
		}, protocol.ErrChecksumMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := newFakePort()
			tc.setup(port)
			_, err := testTransport(t, port).Exchange(context.Background(), 0x02, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if port.closeCalls.Load() != 1 {
				t.Fatalf("close calls = %d", port.closeCalls.Load())
			}
		})
	}
}

func TestSerialCancellationInterruptsRead(t *testing.T) {
	port := newFakePort()
	reading := make(chan struct{})
	var readOnce sync.Once
	port.readFn = func([]byte) (int, error) {
		readOnce.Do(func() { close(reading) })
		<-port.closed
		return 0, errors.New("port closed")
	}
	transport := testTransport(t, port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := transport.Exchange(ctx, 0x02, nil)
		result <- err
	}()
	select {
	case <-reading:
	case <-time.After(3 * time.Second):
		t.Fatal("read did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not unblock serial read")
	}
	if port.closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d", port.closeCalls.Load())
	}
}

func TestSerialWholeResponseDeadline(t *testing.T) {
	port := newFakePort()
	frame, _ := protocol.EncodeCommand(0, []byte("550101046182"))
	port.readFn = func(buffer []byte) (int, error) {
		select {
		case <-port.closed:
			return 0, errors.New("port closed")
		case <-time.After(10 * time.Millisecond):
			if len(frame) == 0 {
				return 0, io.EOF
			}
			buffer[0], frame = frame[0], frame[1:]
			return 1, nil
		}
	}
	transport := testTransport(t, port)
	transport.options.ResponseTimeout = 40 * time.Millisecond
	_, err := transport.Exchange(context.Background(), 0x02, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("whole-response deadline error = %v", err)
	}
	if port.closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d", port.closeCalls.Load())
	}
}

func TestSerialAlreadyCanceledDoesNotOpenPort(t *testing.T) {
	transport := testTransport(t, newFakePort())
	transport.open = func(string, *serial.Mode) (serialPort, error) {
		t.Fatal("port opened for canceled context")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := transport.Exchange(ctx, 0x02, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestSerialSessionKeepsOnePortForMultipleCommands(t *testing.T) {
	first, _ := protocol.EncodeCommand(0, []byte("550101046182"))
	second, _ := protocol.EncodeCommand(0, []byte("1.0.25"))
	port := newFakePort()
	port.reader = bytes.NewReader(append(first, second...))
	transport := testTransport(t, port)
	opener := transport.open
	var opens atomic.Int32
	transport.open = func(name string, mode *serial.Mode) (serialPort, error) {
		opens.Add(1)
		return opener(name, mode)
	}
	var escaped Exchanger
	err := transport.WithSession(context.Background(), func(session Exchanger) error {
		escaped = session
		data, err := session.Exchange(context.Background(), 0x02, nil)
		if err != nil || string(data) != "550101046182" {
			t.Fatalf("first response %q, error %v", data, err)
		}
		if port.closeCalls.Load() != 0 {
			t.Fatal("port closed between commands")
		}
		data, err = session.Exchange(context.Background(), 0x03, nil)
		if err != nil || string(data) != "1.0.25" {
			t.Fatalf("second response %q, error %v", data, err)
		}
		return nil
	})
	if err != nil || opens.Load() != 1 || port.closeCalls.Load() != 1 {
		t.Fatalf("error %v, opens %d, closes %d", err, opens.Load(), port.closeCalls.Load())
	}
	firstRequest, _ := protocol.EncodeCommand(0x02, nil)
	secondRequest, _ := protocol.EncodeCommand(0x03, nil)
	if !bytes.Equal(port.written.Bytes(), append(firstRequest, secondRequest...)) {
		t.Fatalf("written frames % X", port.written.Bytes())
	}
	if _, err := escaped.Exchange(context.Background(), 0x02, nil); !errors.Is(err, errSessionClosed) {
		t.Fatalf("escaped session error %v", err)
	}
}

func TestSerialSessionClosesOnCallbackErrorAndPanic(t *testing.T) {
	callbackError := errors.New("callback failure")
	port := newFakePort()
	err := testTransport(t, port).WithSession(context.Background(), func(Exchanger) error { return callbackError })
	if !errors.Is(err, callbackError) || port.closeCalls.Load() != 1 {
		t.Fatalf("error %v, closes %d", err, port.closeCalls.Load())
	}
	port = newFakePort()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected callback panic")
			}
		}()
		_ = testTransport(t, port).WithSession(context.Background(), func(Exchanger) error { panic("callback panic") })
	}()
	if port.closeCalls.Load() != 1 {
		t.Fatalf("panic close calls %d", port.closeCalls.Load())
	}
}

func TestSerialSessionCannotContinueAfterExchangeFailure(t *testing.T) {
	port := newFakePort()
	port.readFn = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	transport := testTransport(t, port)
	err := transport.WithSession(context.Background(), func(session Exchanger) error {
		if _, err := session.Exchange(context.Background(), 0x02, nil); err == nil {
			t.Fatal("expected first failure")
		}
		if _, err := session.Exchange(context.Background(), 0x03, nil); !errors.Is(err, errSessionFailed) {
			t.Fatalf("second exchange error %v", err)
		}
		return nil
	})
	if err != nil || len(port.written.Bytes()) != 7 || port.closeCalls.Load() != 1 {
		t.Fatalf("error %v, written %d, closes %d", err, port.written.Len(), port.closeCalls.Load())
	}
}

func TestSerialSessionParentCancellationInterruptsIndependentCallContext(t *testing.T) {
	port := newFakePort()
	reading := make(chan struct{})
	var once sync.Once
	port.readFn = func([]byte) (int, error) {
		once.Do(func() { close(reading) })
		<-port.closed
		return 0, errors.New("closed")
	}
	transport := testTransport(t, port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- transport.WithSession(ctx, func(session Exchanger) error {
			_, err := session.Exchange(context.Background(), 0x02, nil)
			return err
		})
	}()
	select {
	case <-reading:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session cancellation did not interrupt read")
	}
	if port.closeCalls.Load() != 1 {
		t.Fatalf("close calls %d", port.closeCalls.Load())
	}
}

func TestSerialSessionUsesFreshTimeoutForEachExchange(t *testing.T) {
	frame, _ := protocol.EncodeCommand(0, nil)
	port := newFakePort()
	port.reader = bytes.NewReader(append(frame, frame...))
	transport := testTransport(t, port)
	transport.options.ResponseTimeout = 20 * time.Millisecond
	err := transport.WithSession(context.Background(), func(session Exchanger) error {
		if _, err := session.Exchange(context.Background(), 0x20, nil); err != nil {
			return err
		}
		time.Sleep(40 * time.Millisecond)
		_, err := session.Exchange(context.Background(), 0x20, nil)
		return err
	})
	if err != nil {
		t.Fatalf("per-command timeout included idle callback time: %v", err)
	}
}
