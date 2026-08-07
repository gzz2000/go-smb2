// Package bulk implements the private, versioned journal transport shared by
// smbrelay and its co-designed go-smb2 backend.
package bulk

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	Directory        = ".smbrelay-bulk-v2"
	CapabilitiesPath = Directory + "/capabilities.json"
	Protocol         = "smbrelay-bulk-journal-v2"
)

var magic = [8]byte{'S', 'M', 'B', 'R', 'B', 'L', 'K', '2'}

type Capabilities struct {
	Protocol      string `json:"protocol"`
	MaxBatchBytes int64  `json:"max_batch_bytes"`
}

type Operation struct {
	Seq    uint64 `json:"seq"`
	Type   string `json:"type"`
	Path   string `json:"path"`
	Target string `json:"target,omitempty"`
	Name   string `json:"name,omitempty"`
	Offset uint64 `json:"offset,omitempty"`
	Length uint64 `json:"length,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
	ATime  int64  `json:"atime,omitempty"`
	MTime  int64  `json:"mtime,omitempty"`
	Data   []byte `json:"-"`
}

type Batch struct {
	RelayID    string      `json:"relay_id"`
	BatchID    string      `json:"batch_id"`
	FirstSeq   uint64      `json:"first_seq"`
	LastSeq    uint64      `json:"last_seq"`
	Operations []Operation `json:"operations"`
}

type Receipt struct {
	Protocol string `json:"protocol"`
	RelayID  string `json:"relay_id"`
	BatchID  string `json:"batch_id"`
	LastSeq  uint64 `json:"last_seq"`
	Hash     string `json:"hash"`
}

type wireOperation struct {
	Seq, Offset, Length, DataOffset, DataLength uint64
	Type, Path, Target, Name                    string
	Mode                                        uint32
	ATime, MTime                                int64
}

type manifest struct {
	RelayID, BatchID  string
	FirstSeq, LastSeq uint64
	Operations        []wireOperation
}

func Encode(batch *Batch) ([]byte, string, error) {
	var payload bytes.Buffer
	m := manifest{RelayID: batch.RelayID, BatchID: batch.BatchID, FirstSeq: batch.FirstSeq, LastSeq: batch.LastSeq}
	for _, op := range batch.Operations {
		wire := wireOperation{Seq: op.Seq, Type: op.Type, Path: op.Path, Target: op.Target, Name: op.Name, Offset: op.Offset, Length: op.Length, Mode: op.Mode, ATime: op.ATime, MTime: op.MTime}
		if len(op.Data) != 0 {
			wire.DataOffset = uint64(payload.Len())
			wire.DataLength = uint64(len(op.Data))
			_, _ = payload.Write(op.Data)
		}
		m.Operations = append(m.Operations, wire)
	}
	manifestBytes, err := json.Marshal(&m)
	if err != nil {
		return nil, "", err
	}
	if uint64(len(manifestBytes)) > uint64(^uint32(0)) {
		return nil, "", errors.New("bulk manifest is too large")
	}
	body := append(append([]byte(nil), manifestBytes...), payload.Bytes()...)
	sum := sha256.Sum256(body)
	var out bytes.Buffer
	_, _ = out.Write(magic[:])
	_ = binary.Write(&out, binary.BigEndian, uint32(len(manifestBytes)))
	_ = binary.Write(&out, binary.BigEndian, uint64(payload.Len()))
	_, _ = out.Write(sum[:])
	_, _ = out.Write(body)
	return out.Bytes(), hex.EncodeToString(sum[:]), nil
}

func Decode(data []byte) (*Batch, string, error) {
	const headerSize = 8 + 4 + 8 + sha256.Size
	if len(data) < headerSize || !bytes.Equal(data[:8], magic[:]) {
		return nil, "", errors.New("invalid bulk journal header")
	}
	manifestLength := binary.BigEndian.Uint32(data[8:12])
	payloadLength := binary.BigEndian.Uint64(data[12:20])
	wantSum := data[20 : 20+sha256.Size]
	body := data[headerSize:]
	if uint64(len(body)) != uint64(manifestLength)+payloadLength {
		return nil, "", errors.New("invalid bulk journal length")
	}
	sum := sha256.Sum256(body)
	if !bytes.Equal(sum[:], wantSum) {
		return nil, "", errors.New("bulk journal checksum mismatch")
	}
	var m manifest
	if err := json.Unmarshal(body[:manifestLength], &m); err != nil {
		return nil, "", fmt.Errorf("decode bulk manifest: %w", err)
	}
	payload := body[manifestLength:]
	batch := &Batch{RelayID: m.RelayID, BatchID: m.BatchID, FirstSeq: m.FirstSeq, LastSeq: m.LastSeq}
	for _, wire := range m.Operations {
		op := Operation{Seq: wire.Seq, Type: wire.Type, Path: wire.Path, Target: wire.Target, Name: wire.Name, Offset: wire.Offset, Length: wire.Length, Mode: wire.Mode, ATime: wire.ATime, MTime: wire.MTime}
		if wire.DataLength != 0 {
			end := wire.DataOffset + wire.DataLength
			if end < wire.DataOffset || end > uint64(len(payload)) {
				return nil, "", io.ErrUnexpectedEOF
			}
			op.Data = append([]byte(nil), payload[wire.DataOffset:end]...)
		}
		batch.Operations = append(batch.Operations, op)
	}
	return batch, hex.EncodeToString(sum[:]), nil
}

func ReceiptPath(relayID string) string {
	sum := sha256.Sum256([]byte(relayID))
	return fmt.Sprintf("%s/receipts/%x.json", Directory, sum[:16])
}

func ReadyPath(batchID string) string {
	sum := sha256.Sum256([]byte(batchID))
	return fmt.Sprintf("%s/inbox/%x.ready", Directory, sum[:16])
}
