package transport

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/sirupsen/logrus"
)

func newTCPForTest(t *testing.T, address string) *TCP {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	transport, err := NewTCP(TCPOptions{
		Address: address, ConnectTimeout: time.Second, ResponseTimeout: time.Second,
	}, logrus.NewEntry(logger))
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func tcpTestServer(t *testing.T, handle func(net.Conn)) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepts atomic.Int32
	var mutex sync.Mutex
	var connections []net.Conn
	var handlers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			mutex.Lock()
			connections = append(connections, conn)
			mutex.Unlock()
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer conn.Close()
				handle(conn)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		mutex.Lock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		mutex.Unlock()
		handlers.Wait()
	})
	return listener.Addr().String(), &accepts
}

func tcpReadRequest(t *testing.T, conn net.Conn) bool {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	request := make([]byte, 7)
	if _, err := io.ReadFull(conn, request); err != nil {
		t.Errorf("read command: %v", err)
		return false
	}
	if got := hex.EncodeToString(request); got != "b629000102efdf" {
		t.Errorf("request frame %s", got)
		return false
	}
	return true
}

func capturedTCPSerialFrame() []byte {
	frame, _ := hex.DecodeString("b629000d0035353031303130343631383252de")
	return frame
}

func TestTCPFragmentedResponseAndFreshConnections(t *testing.T) {
	clientClosed := make(chan struct{}, 2)
	address, accepts := tcpTestServer(t, func(conn net.Conn) {
		if !tcpReadRequest(t, conn) {
			return
		}
		frame := capturedTCPSerialFrame()
		for i := 0; i < len(frame); i += 3 {
			end := i + 3
			if end > len(frame) {
				end = len(frame)
			}
			if _, err := conn.Write(frame[i:end]); err != nil {
				return
			}
		}
		// The server deliberately keeps this connection open. A complete frame,
		// rather than server EOF, must let Exchange return and close its socket.
		var extra [1]byte
		if _, err := conn.Read(extra[:]); errors.Is(err, io.EOF) {
			clientClosed <- struct{}{}
		}
	})
	transport := newTCPForTest(t, address)
	for i := 0; i < 2; i++ {
		data, err := transport.Exchange(context.Background(), 0x02, nil)
		if err != nil || string(data) != "550101046182" {
			t.Fatalf("data %q, error %v", data, err)
		}
	}
	if accepts.Load() != 2 {
		t.Fatalf("connections %d, want 2", accepts.Load())
	}
	for i := 0; i < 2; i++ {
		select {
		case <-clientClosed:
		case <-time.After(time.Second):
			t.Fatal("client did not close its command connection")
		}
	}
}

func TestTCPProtocolFailuresArePreserved(t *testing.T) {
	badCRC := capturedTCPSerialFrame()
	badCRC[len(badCRC)-1] ^= 1
	rejected, _ := protocol.EncodeCommand(1, []byte{0x28})
	cases := []struct {
		name       string
		response   []byte
		want       error
		deviceCode byte
	}{
		{"CRC", badCRC, protocol.ErrChecksumMismatch, 0},
		{"invalid length", []byte{0xb6, 0x29, 0x04, 0x01}, protocol.ErrMalformedFrame, 0},
		{"invalid marker", []byte{0xaa, 0xbb, 0, 1}, protocol.ErrMalformedFrame, 0},
		{"truncated body", []byte{0xb6, 0x29, 0, 0x0d, 0, 1}, io.ErrUnexpectedEOF, 0},
		{"empty response", nil, io.EOF, 0},
		{"device rejection", rejected, nil, 0x28},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			address, _ := tcpTestServer(t, func(conn net.Conn) {
				if !tcpReadRequest(t, conn) {
					return
				}
				_, _ = conn.Write(tc.response)
			})
			_, err := newTCPForTest(t, address).Exchange(context.Background(), 0x02, nil)
			if tc.deviceCode != 0 {
				var rejection *protocol.DeviceError
				if !errors.As(err, &rejection) || rejection.Code != tc.deviceCode {
					t.Fatalf("error %v", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
			var notSent *NotSentError
			if errors.As(err, &notSent) {
				t.Fatal("response error was incorrectly certified as not sent")
			}
		})
	}
}

func TestTCPCancellationInterruptsResponseRead(t *testing.T) {
	reading := make(chan struct{})
	address, _ := tcpTestServer(t, func(conn net.Conn) {
		if !tcpReadRequest(t, conn) {
			return
		}
		close(reading)
		_, _ = conn.Read(make([]byte, 1))
	})
	transport := newTCPForTest(t, address)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := transport.Exchange(ctx, 0x02, nil); done <- err }()
	select {
	case <-reading:
	case <-time.After(time.Second):
		t.Fatal("server did not receive request")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v", err)
		}
		var notSent *NotSentError
		if errors.As(err, &notSent) {
			t.Fatal("sent command incorrectly certified as not sent")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt TCP read")
	}
}

func TestTCPDeadlineIsSharedAcrossFragmentedReads(t *testing.T) {
	address, _ := tcpTestServer(t, func(conn net.Conn) {
		if !tcpReadRequest(t, conn) {
			return
		}
		for _, value := range capturedTCPSerialFrame() {
			time.Sleep(20 * time.Millisecond)
			if _, err := conn.Write([]byte{value}); err != nil {
				return
			}
		}
	})
	transport := newTCPForTest(t, address)
	transport.options.ResponseTimeout = 60 * time.Millisecond
	started := time.Now()
	_, err := transport.Exchange(context.Background(), 0x02, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v", err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("deadline extended by partial traffic: %v", elapsed)
	}
}

func TestTCPCanceledContextAndInvalidFrameDoNotDial(t *testing.T) {
	transport := newTCPForTest(t, "localhost:12345")
	transport.dial = func(context.Context, string, string) (net.Conn, error) { t.Fatal("unexpected dial"); return nil, nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := transport.Exchange(ctx, 0x02, nil)
	var notSent *NotSentError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &notSent) {
		t.Fatalf("canceled error %v", err)
	}
	_, err = transport.Exchange(context.Background(), 0x02, make([]byte, protocol.MaxPayloadSize+1))
	if !errors.Is(err, protocol.ErrMalformedFrame) {
		t.Fatalf("payload error %v", err)
	}
}

func TestTCPDialRefusalIsNotRetried(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	transport := newTCPForTest(t, address)
	dial := transport.dial
	calls := 0
	transport.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		calls++
		return dial(ctx, network, address)
	}
	_, err = transport.Exchange(context.Background(), 0x02, nil)
	var notSent *NotSentError
	if err == nil || !errors.As(err, &notSent) || calls != 1 {
		t.Fatalf("error %v, dial calls %d", err, calls)
	}
}

func TestTCPResponseDeadlineIncludesDial(t *testing.T) {
	transport := newTCPForTest(t, "localhost:12345")
	transport.options.ResponseTimeout = 30 * time.Millisecond
	transport.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	started := time.Now()
	_, err := transport.Exchange(context.Background(), 0x02, nil)
	var notSent *NotSentError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &notSent) {
		t.Fatalf("error %v", err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("dial exceeded whole exchange deadline: %v", elapsed)
	}
}

type tcpStubConn struct {
	reader        io.Reader
	write         func([]byte) (int, error)
	deadlineError error
	setDeadline   func(time.Time) error
	written       bytes.Buffer
	closes        atomic.Int32
}

func (c *tcpStubConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *tcpStubConn) Write(p []byte) (int, error) {
	if c.write != nil {
		return c.write(p)
	}
	return c.written.Write(p)
}
func (c *tcpStubConn) Close() error         { c.closes.Add(1); return nil }
func (c *tcpStubConn) LocalAddr() net.Addr  { return &net.TCPAddr{} }
func (c *tcpStubConn) RemoteAddr() net.Addr { return &net.TCPAddr{} }
func (c *tcpStubConn) SetDeadline(deadline time.Time) error {
	if c.setDeadline != nil {
		return c.setDeadline(deadline)
	}
	return c.deadlineError
}
func (c *tcpStubConn) SetReadDeadline(deadline time.Time) error  { return c.SetDeadline(deadline) }
func (c *tcpStubConn) SetWriteDeadline(deadline time.Time) error { return c.SetDeadline(deadline) }

func TestTCPPartialWritesAndDefinitePreSendFailures(t *testing.T) {
	broken := errors.New("simulated socket failure")
	cases := []struct {
		name    string
		setup   func(*tcpStubConn)
		want    error
		notSent bool
	}{
		{"short writes", func(conn *tcpStubConn) {
			conn.write = func(p []byte) (int, error) {
				if len(p) > 2 {
					p = p[:2]
				}
				return conn.written.Write(p)
			}
		}, nil, false},
		{"zero write", func(conn *tcpStubConn) { conn.write = func([]byte) (int, error) { return 0, nil } }, io.ErrShortWrite, true},
		{"write fails before bytes", func(conn *tcpStubConn) { conn.write = func([]byte) (int, error) { return 0, broken } }, broken, true},
		{"write fails after partial bytes", func(conn *tcpStubConn) { conn.write = func(p []byte) (int, error) { return 2, broken } }, broken, false},
		{"later write fails", func(conn *tcpStubConn) {
			first := true
			conn.write = func(p []byte) (int, error) {
				if first {
					first = false
					return 2, nil
				}
				return 0, broken
			}
		}, broken, false},
		{"deadline setting fails", func(conn *tcpStubConn) { conn.deadlineError = broken }, broken, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &tcpStubConn{reader: bytes.NewReader(capturedTCPSerialFrame())}
			tc.setup(conn)
			transport := newTCPForTest(t, "localhost:12345")
			transport.dial = func(context.Context, string, string) (net.Conn, error) { return conn, nil }
			data, err := transport.Exchange(context.Background(), 0x02, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
			var notSent *NotSentError
			if errors.As(err, &notSent) != tc.notSent {
				t.Fatalf("pre-send classification %v, error %v", notSent, err)
			}
			if err == nil && (string(data) != "550101046182" || hex.EncodeToString(conn.written.Bytes()) != "b629000102efdf") {
				t.Fatalf("data %q, writes % X", data, conn.written.Bytes())
			}
			if conn.closes.Load() != 1 {
				t.Fatalf("close calls %d", conn.closes.Load())
			}
		})
	}
}

func TestTCPCancellationBeforeFirstWriteIsDefinite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &tcpStubConn{reader: bytes.NewReader(capturedTCPSerialFrame()), setDeadline: func(time.Time) error { cancel(); return nil }}
	transport := newTCPForTest(t, "localhost:12345")
	transport.dial = func(context.Context, string, string) (net.Conn, error) { return conn, nil }
	_, err := transport.Exchange(ctx, 0x02, nil)
	var notSent *NotSentError
	if !errors.As(err, &notSent) || !errors.Is(err, context.Canceled) || conn.written.Len() != 0 || conn.closes.Load() != 1 {
		t.Fatalf("error %v, writes %d, closes %d", err, conn.written.Len(), conn.closes.Load())
	}
}

func TestTCPConstructorValidatesAddressAndTimeouts(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9100", "localhost:9100", "terminal-fa.lan:9100", "[::1]:9100"} {
		if _, err := NewTCP(TCPOptions{Address: address, ConnectTimeout: time.Second, ResponseTimeout: time.Second}, nil); err != nil {
			t.Errorf("valid address %q: %v", address, err)
		}
	}
	for _, address := range []string{"", ":9100", "localhost", "http://localhost:9100", "localhost:0", "localhost:65536", "localhost:+123", "localhost:http", "host name:9100", "host/path:9100", "host\\name:9100"} {
		if _, err := NewTCP(TCPOptions{Address: address, ConnectTimeout: time.Second, ResponseTimeout: time.Second}, nil); err == nil {
			t.Errorf("accepted invalid address %q", address)
		}
	}
	if _, err := NewTCP(TCPOptions{Address: "localhost:9100", ConnectTimeout: 0, ResponseTimeout: time.Second}, nil); err == nil {
		t.Error("accepted zero connect timeout")
	}
	if _, err := NewTCP(TCPOptions{Address: "localhost:9100", ConnectTimeout: time.Second, ResponseTimeout: -time.Second}, nil); err == nil {
		t.Error("accepted negative response timeout")
	}
}
