package smb2

import "testing"

func TestShortPacketIsInvalidWithoutPanicking(t *testing.T) {
	for length := 0; length < 4; length++ {
		packet := PacketCodec(make([]byte, length))
		if !packet.IsInvalid() {
			t.Fatalf("packet length %d was accepted", length)
		}
		if packet.IsSmb1() {
			t.Fatalf("packet length %d was identified as SMB1", length)
		}
		if protocol := packet.ProtocolId(); protocol != nil {
			t.Fatalf("packet length %d protocol ID=%v, want nil", length, protocol)
		}
	}
}

func TestMinimalSMB1NegotiatePacketRemainsValid(t *testing.T) {
	packet := PacketCodec{0xff, 'S', 'M', 'B', SMB_COM_NEGOTIATE}
	if packet.IsInvalid() {
		t.Fatal("minimal SMB1 negotiate packet was rejected")
	}
}

func TestEncodeHeaderUsesAsyncIdWhenPresent(t *testing.T) {
	hdr := PacketHeader{
		Command:   SMB2_READ,
		Flags:     SMB2_FLAGS_SERVER_TO_REDIR | SMB2_FLAGS_ASYNC_COMMAND,
		MessageId: 42,
		AsyncId:   0x1122334455667788,
		TreeId:    0xaabbccdd,
		SessionId: 0x0102030405060708,
	}
	pkt := make([]byte, 64)

	hdr.encodeHeader(pkt)
	p := PacketCodec(pkt)

	if got := p.AsyncId(); got != hdr.AsyncId {
		t.Fatalf("AsyncId = 0x%x, want 0x%x", got, hdr.AsyncId)
	}
	if got := p.TreeId(); got == hdr.TreeId {
		t.Fatalf("TreeId was encoded into async header: 0x%x", got)
	}
}
