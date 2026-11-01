package terminalfa

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestPublicTCPClientReadsSerialAndClosesWithoutWaitingForEOF(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		request := make([]byte, 7)
		if _, err := io.ReadFull(conn, request); err != nil {
			done <- err
			return
		}
		expected, _ := hex.DecodeString("b629000102efdf")
		if !bytes.Equal(request, expected) {
			done <- fmt.Errorf("request % X", request)
			return
		}
		response, _ := hex.DecodeString("b629000d0035353031303130343631383252de")
		if _, err := conn.Write(response); err != nil {
			done <- err
			return
		}
		// The server deliberately does not close first. A framed response is
		// complete independently of EOF and the host must close this connection.
		var extra [1]byte
		n, err := conn.Read(extra[:])
		if n != 0 || err != io.EOF {
			done <- fmt.Errorf("host did not close after response: %d, %v", n, err)
			return
		}
		done <- nil
	}()
	client, err := NewTCPClient(TCPOptions{Address: listener.Addr().String(), ConnectTimeout: time.Second, ResponseTimeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := client.SerialNumber(context.Background())
	if err != nil || serial != "550101046182" {
		t.Fatalf("serial %q, %v", serial, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
