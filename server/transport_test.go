package smb2

import (
	"net"
	"strings"
	"testing"
)

func TestDirectTCPRejectsZeroLengthPacket(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	written := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte{0, 0, 0, 0})
		written <- err
	}()

	_, err := direct(server).ReadSize()
	if err == nil || !strings.Contains(err.Error(), "zero-length") {
		t.Fatalf("ReadSize error=%v, want zero-length transport error", err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}
