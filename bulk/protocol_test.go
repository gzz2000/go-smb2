package bulk

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeAuthenticatedBatch(t *testing.T) {
	secret := []byte("shared-test-secret")
	want := &Batch{RelayID: "relay", BatchID: "batch", FirstSeq: 7, LastSeq: 8, Operations: []Operation{
		{Seq: 7, Type: "write", Path: "bundle/bands/1", Offset: 11, Length: 4, Data: []byte("data")},
		{Seq: 8, Type: "rename", Path: "old", Target: "new"},
	}}
	encoded, hash, err := Encode(want, secret)
	if err != nil {
		t.Fatal(err)
	}
	got, gotHash, err := Decode(encoded, secret)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != hash || got.RelayID != want.RelayID || got.BatchID != want.BatchID || len(got.Operations) != 2 || !bytes.Equal(got.Operations[0].Data, want.Operations[0].Data) {
		t.Fatalf("decoded batch=%+v hash=%q", got, gotHash)
	}
	encoded[len(encoded)-1] ^= 0x80
	if _, _, err := Decode(encoded, secret); err == nil {
		t.Fatal("tampered package passed authentication")
	}
}
