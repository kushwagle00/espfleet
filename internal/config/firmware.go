package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// FirmwareError identifies a fault in the shared artifact, not a board or cable.
// Batch execution must stop scheduling new boards when this error is returned.
type FirmwareError struct{ Err error }

func (e *FirmwareError) Error() string { return "firmware: " + e.Err.Error() }
func (e *FirmwareError) Unwrap() error { return e.Err }

// ValidateFirmware checks ESP application/bootloader image structure, checksum,
// and any embedded digest. Raw partition data is permitted. Merged images are
// validated at their first ESP header; arbitrary partition layouts are not inferred.
func ValidateFirmware(data []byte, chip string) error {
	if bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}) {
		return errors.New("ELF input is not a flashable binary")
	}
	if _, err := HeaderFlashSize(data, chip); err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("empty firmware")
	}
	if data[0] != 0xe9 {
		return nil
	}
	segments := int(data[1])
	if segments < 1 || segments > 16 {
		return errors.New("invalid ESP image segment count")
	}
	pos := uint64(24)
	checksum := byte(0xef)
	for i := 0; i < segments; i++ {
		if pos+8 > uint64(len(data)) {
			return errors.New("truncated ESP segment header")
		}
		n := uint64(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		if pos+n > uint64(len(data)) {
			return errors.New("truncated ESP segment data")
		}
		for _, b := range data[pos : pos+n] {
			checksum ^= b
		}
		pos += n
	}
	end := (pos + 16) &^ uint64(15)
	if end > uint64(len(data)) {
		return errors.New("missing ESP image checksum")
	}
	if data[end-1] != checksum {
		return errors.New("ESP image checksum mismatch")
	}
	if data[23] > 1 {
		return errors.New("invalid ESP image digest flag")
	}
	if data[23] == 1 {
		if end+32 > uint64(len(data)) {
			return errors.New("missing ESP image SHA-256 digest")
		}
		sum := sha256.Sum256(data[:end])
		if !bytes.Equal(sum[:], data[end:end+32]) {
			return fmt.Errorf("embedded ESP image SHA-256 mismatch")
		}
	}
	return nil
}
