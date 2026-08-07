package bulk

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/macos-fuse-t/go-smb2/internal/xattrstore"
)

func TestCommitReadyAppliesAndRecoversIdempotently(t *testing.T) {
	root := t.TempDir()
	const maximum = int64(8 << 20)
	if err := Initialize(root, maximum); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("000000"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b"), []byte("111111"), 0o600); err != nil {
		t.Fatal(err)
	}
	batch := &Batch{RelayID: "relay-a", BatchID: "10-12", FirstSeq: 10, LastSeq: 12, Operations: []Operation{
		{Seq: 10, Type: "write", Path: "a", Offset: 0, Length: 2, Data: []byte("AA")},
		{Seq: 11, Type: "write", Path: "b", Offset: 2, Length: 1, Data: []byte("B")},
		{Seq: 12, Type: "write", Path: "a", Offset: 1, Length: 2, Data: []byte("ZZ")},
	}}
	encoded, hash, err := Encode(batch)
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, filepath.FromSlash(ReadyPath(batch.BatchID)))
	if err := os.WriteFile(ready, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	receipt, err := CommitReady(root, ready, maximum)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.LastSeq != 12 || receipt.Hash != hash {
		t.Fatalf("receipt=%+v", receipt)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a")); string(got) != "AZZ000" {
		t.Fatalf("a=%q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "b")); string(got) != "11B111" {
		t.Fatalf("b=%q", got)
	}
	// Losing the relay's local completion after receiving the backend response
	// resends the same package. The durable receipt must make that harmless.
	if err := os.WriteFile(ready, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if retry, err := CommitReady(root, ready, maximum); err != nil || retry.LastSeq != 12 {
		t.Fatalf("idempotent retry receipt=%+v err=%v", retry, err)
	}

	rename := &Batch{RelayID: "relay-a", BatchID: "13", FirstSeq: 13, LastSeq: 13, Operations: []Operation{{Seq: 13, Type: "rename", Path: "a", Target: "renamed"}}}
	renameData, _, _ := Encode(rename)
	renameReady := filepath.Join(root, filepath.FromSlash(ReadyPath(rename.BatchID)))
	if err := os.WriteFile(renameReady, renameData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitReady(root, renameReady, maximum); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "renamed")); err != nil {
		t.Fatal(err)
	}

	gap := &Batch{RelayID: "relay-a", BatchID: "gap", FirstSeq: 20, LastSeq: 20, Operations: []Operation{{Seq: 20, Type: "truncate", Path: "renamed", Length: 1}}}
	gapData, _, _ := Encode(gap)
	gapReady := filepath.Join(root, filepath.FromSlash(ReadyPath(gap.BatchID)))
	if err := os.WriteFile(gapReady, gapData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitReady(root, gapReady, maximum); err == nil {
		t.Fatal("sequence gap was accepted")
	}
}

func TestCommitReadyAppliesAppleXattrReplacement(t *testing.T) {
	root := t.TempDir()
	const maximum = int64(8 << 20)
	if err := Initialize(root, maximum); err != nil {
		t.Fatal(err)
	}
	const bundle = "Zizheng's MacBook Air.sparsebundle"
	if err := os.Mkdir(filepath.Join(root, bundle), 0o700); err != nil {
		t.Fatal(err)
	}

	want := []byte("0123456789abcdef")
	batch := &Batch{
		RelayID:  "relay-xattr",
		BatchID:  "5-6",
		FirstSeq: 5,
		LastSeq:  6,
		Operations: []Operation{
			{Seq: 5, Type: "setxattr", Path: bundle, Name: "com.apple.lastuseddate#PS", Data: []byte{}},
			{Seq: 6, Type: "setxattr", Path: bundle, Name: "com.apple.lastuseddate#PS", Data: want},
		},
	}
	encoded, _, err := Encode(batch)
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, filepath.FromSlash(ReadyPath(batch.BatchID)))
	if err := os.WriteFile(ready, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	receipt, err := CommitReady(root, ready, maximum)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.LastSeq != 6 {
		t.Fatalf("receipt = %+v", receipt)
	}
	got, err := xattrstore.Get(filepath.Join(root, bundle), "com.apple.lastuseddate#PS")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xattr = %x, want %x", got, want)
	}
}
