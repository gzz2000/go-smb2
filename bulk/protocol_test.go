package bulk

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeChecksummedBatch(t *testing.T) {
	want := &Batch{RelayID: "relay", BatchID: "batch", FirstSeq: 7, LastSeq: 8, Operations: []Operation{
		{Seq: 7, Type: "write", Path: "bundle/bands/1", Offset: 11, Length: 4, Data: []byte("data")},
		{Seq: 8, Type: "rename", Path: "old", Target: "new"},
	}}
	encoded, hash, err := Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, gotHash, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != hash || got.RelayID != want.RelayID || got.BatchID != want.BatchID || len(got.Operations) != 2 || !bytes.Equal(got.Operations[0].Data, want.Operations[0].Data) {
		t.Fatalf("decoded batch=%+v hash=%q", got, gotHash)
	}
	encoded[len(encoded)-1] ^= 0x80
	if _, _, err := Decode(encoded); err == nil {
		t.Fatal("tampered package passed checksum validation")
	}
}
