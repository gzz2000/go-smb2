package bulk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/macos-fuse-t/go-smb2/internal/xattrstore"
	"github.com/pkg/xattr"
)

func Initialize(root string, maxBatchBytes int64) error {
	for _, dir := range []string{Directory, Directory + "/inbox", Directory + "/receipts"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o700); err != nil {
			return err
		}
	}
	capabilities, _ := json.Marshal(Capabilities{Protocol: Protocol, MaxBatchBytes: maxBatchBytes})
	return writeAtomic(filepath.Join(root, filepath.FromSlash(CapabilitiesPath)), capabilities)
}

func CommitReady(root, readyPath string, maxBatchBytes int64) (*Receipt, error) {
	info, err := os.Stat(readyPath)
	if err != nil {
		return nil, err
	}
	if maxBatchBytes > 0 && info.Size() > maxBatchBytes {
		return nil, fmt.Errorf("bulk journal package is %d bytes; maximum is %d", info.Size(), maxBatchBytes)
	}
	// The SMB rename is the upload commit point. Persist that directory entry
	// before applying anything so a backend crash always leaves either the
	// ready package or a durable receipt to drive idempotent recovery.
	if err := syncDirectory(filepath.Dir(readyPath)); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(readyPath)
	if err != nil {
		return nil, err
	}
	batch, hash, err := Decode(data)
	if err != nil {
		return nil, err
	}
	if err := validateBatch(batch); err != nil {
		return nil, err
	}
	receiptPath := filepath.Join(root, filepath.FromSlash(ReceiptPath(batch.RelayID)))
	if previous, readErr := readReceipt(receiptPath); readErr == nil {
		if previous.Protocol != Protocol || previous.RelayID != batch.RelayID {
			return nil, errors.New("invalid previous bulk receipt")
		}
		if previous.LastSeq >= batch.LastSeq {
			_ = os.Remove(readyPath)
			_ = syncDirectory(filepath.Dir(readyPath))
			return previous, nil
		}
		if batch.FirstSeq > previous.LastSeq+1 {
			return nil, fmt.Errorf("bulk journal sequence gap: receipt=%d batch=%d", previous.LastSeq, batch.FirstSeq)
		}
	}
	if err := apply(root, batch); err != nil {
		return nil, err
	}
	receipt := &Receipt{Protocol: Protocol, RelayID: batch.RelayID, BatchID: batch.BatchID, LastSeq: batch.LastSeq, Hash: hash}
	receiptData, _ := json.Marshal(receipt)
	if err := writeAtomic(receiptPath, receiptData); err != nil {
		return nil, err
	}
	_ = os.Remove(readyPath)
	_ = syncDirectory(filepath.Dir(readyPath))
	return receipt, nil
}

func apply(root string, batch *Batch) error {
	for i := 0; i < len(batch.Operations); {
		if batch.Operations[i].Type == "write" {
			end := i + 1
			for end < len(batch.Operations) && batch.Operations[end].Type == "write" {
				end++
			}
			if err := applyWriteRun(root, batch.Operations[i:end]); err != nil {
				return err
			}
			i = end
			continue
		}
		op := batch.Operations[i]
		i++
		p, err := resolve(root, op.Path)
		if err != nil {
			return err
		}
		switch op.Type {
		case "create":
			f, createErr := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_RDWR, os.FileMode(op.Mode))
			if createErr == nil {
				if createErr = f.Sync(); createErr == nil {
					createErr = f.Close()
				} else {
					_ = f.Close()
				}
			} else if errors.Is(createErr, os.ErrExist) {
				info, statErr := os.Lstat(p)
				if statErr == nil && info.Mode().IsRegular() {
					createErr = nil
				}
			}
			if createErr != nil {
				return createErr
			}
			if err := syncDirectory(filepath.Dir(p)); err != nil {
				return err
			}
		case "mkdir":
			if err := os.Mkdir(p, os.FileMode(op.Mode)); err != nil {
				if !errors.Is(err, os.ErrExist) {
					return err
				}
				if info, statErr := os.Stat(p); statErr != nil || !info.IsDir() {
					return err
				}
			}
			if err := syncDirectory(filepath.Dir(p)); err != nil {
				return err
			}
		case "truncate":
			if err := os.Truncate(p, int64(op.Length)); err != nil {
				return err
			}
			if err := syncFile(p); err != nil {
				return err
			}
		case "setattr":
			if err := os.Chmod(p, os.FileMode(op.Mode)); err != nil {
				return err
			}
			if err := os.Chtimes(p, time.Unix(0, op.ATime), time.Unix(0, op.MTime)); err != nil {
				return err
			}
			if err := syncFile(p); err != nil {
				return err
			}
		case "rename":
			target, err := resolve(root, op.Target)
			if err != nil {
				return err
			}
			if err := os.Rename(p, target); err != nil {
				if _, oldErr := os.Lstat(p); !errors.Is(oldErr, os.ErrNotExist) {
					return err
				}
				if _, targetErr := os.Lstat(target); targetErr != nil {
					return err
				}
			}
			if err := syncDirectory(filepath.Dir(p)); err != nil {
				return err
			}
			if filepath.Dir(target) != filepath.Dir(p) {
				if err := syncDirectory(filepath.Dir(target)); err != nil {
					return err
				}
			}
		case "remove":
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := syncDirectory(filepath.Dir(p)); err != nil {
				return err
			}
		case "symlink":
			_ = os.Remove(p)
			if err := os.Symlink(op.Target, p); err != nil {
				return err
			}
			if err := syncDirectory(filepath.Dir(p)); err != nil {
				return err
			}
		case "setxattr":
			if err := xattrstore.Set(p, op.Name, op.Data); err != nil {
				return err
			}
			if err := syncFile(p); err != nil {
				return err
			}
		case "removexattr":
			if err := xattrstore.Remove(p, op.Name); err != nil && !errors.Is(err, xattr.ENOATTR) {
				return err
			}
			if err := syncFile(p); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported bulk operation %q", op.Type)
		}
	}
	return nil
}

// Writes commute across distinct files until the next namespace/metadata
// mutation. Grouping that run opens and fsyncs each file once while retaining
// journal order for overlapping writes to the same file.
func applyWriteRun(root string, ops []Operation) error {
	order := make([]string, 0)
	byPath := make(map[string][]Operation)
	for _, op := range ops {
		p, err := resolve(root, op.Path)
		if err != nil {
			return err
		}
		if _, seen := byPath[p]; !seen {
			order = append(order, p)
		}
		byPath[p] = append(byPath[p], op)
	}
	for _, p := range order {
		f, err := os.OpenFile(p, os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		for _, op := range byPath[p] {
			n, writeErr := f.WriteAt(op.Data, int64(op.Offset))
			if writeErr == nil && n != len(op.Data) {
				writeErr = io.ErrShortWrite
			}
			if writeErr != nil {
				_ = f.Close()
				return writeErr
			}
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

func validateBatch(batch *Batch) error {
	if batch == nil || batch.RelayID == "" || batch.BatchID == "" || len(batch.Operations) == 0 {
		return errors.New("invalid empty bulk journal batch")
	}
	if batch.FirstSeq == 0 || batch.LastSeq < batch.FirstSeq {
		return errors.New("invalid bulk journal sequence bounds")
	}
	var previous uint64
	for i, op := range batch.Operations {
		if op.Seq < batch.FirstSeq || op.Seq > batch.LastSeq || (i != 0 && op.Seq < previous) {
			return errors.New("bulk journal operations are outside sequence order")
		}
		previous = op.Seq
		if op.Type == "write" && uint64(len(op.Data)) != op.Length {
			return fmt.Errorf("bulk write %d length mismatch", op.Seq)
		}
	}
	return nil
}

func resolve(root, name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	clean := filepath.Clean(filepath.Join(root, filepath.FromSlash(name)))
	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("bulk path escapes share root")
	}
	return clean, nil
}

func readReceipt(path string) (*Receipt, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var receipt Receipt
	err = json.Unmarshal(b, &receipt)
	return &receipt, err
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	err = dir.Sync()
	if closeErr := dir.Close(); err == nil {
		err = closeErr
	}
	return err
}
