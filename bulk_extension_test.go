package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/macos-fuse-t/go-smb2/bulk"
)

func TestPassthroughRenameCommitsBulkPackage(t *testing.T) {
	root := t.TempDir()
	secret := "vfs-shared-secret"
	t.Setenv("SMBRELAY_BULK_SECRET", secret)
	fs := NewPassthroughFS(root)
	batch := &bulk.Batch{RelayID: "relay-vfs", BatchID: "batch-vfs", FirstSeq: 1, LastSeq: 2, Operations: []bulk.Operation{
		{Seq: 1, Type: "create", Path: "band", Mode: 0o600},
		{Seq: 2, Type: "write", Path: "band", Offset: 3, Length: 4, Data: []byte("data")},
	}}
	encoded, _, err := bulk.Encode(batch, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	tmp := bulk.Directory + "/inbox/upload.tmp"
	handle, err := fs.Open(tmp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := fs.Write(handle, encoded, 0, 0); err != nil || n != len(encoded) {
		t.Fatalf("write package n=%d err=%v", n, err)
	}
	if err := fs.FSync(handle); err != nil {
		t.Fatal(err)
	}
	if err := fs.Rename(handle, bulk.ReadyPath(batch.BatchID), 0); err != nil {
		t.Fatal(err)
	}
	if err := fs.Close(handle); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "band"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "\x00\x00\x00data" {
		t.Fatalf("backend band=%q", got)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(bulk.ReceiptPath(batch.RelayID)))); err != nil {
		t.Fatal(err)
	}
}
