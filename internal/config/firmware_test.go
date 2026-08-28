package config

import (
	"crypto/sha256"
	"testing"
)

func validImage() []byte {
	b := make([]byte, 80)
	b[0] = 0xe9
	b[1] = 1
	b[3] = 0x20
	b[12] = 9
	b[23] = 1
	b[28] = 1
	b[32] = 0x42
	b[47] = 0xef ^ 0x42
	s := sha256.Sum256(b[:48])
	copy(b[48:], s[:])
	return b
}
func TestFirmwareStructure(t *testing.T) {
	if err := ValidateFirmware(validImage(), "esp32s3"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func([]byte) []byte
	}{
		{"segment count", func(b []byte) []byte { b[1] = 0; return b }},
		{"truncated segment", func(b []byte) []byte { b[31] = 0xff; return b }},
		{"checksum", func(b []byte) []byte { b[47] ^= 1; return b }},
		{"digest", func(b []byte) []byte { b[48] ^= 1; return b }},
		{"truncated digest", func(b []byte) []byte { return b[:60] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateFirmware(tc.change(validImage()), "esp32s3"); err == nil {
				t.Fatal("corrupt image accepted")
			}
		})
	}
	if err := ValidateFirmware([]byte{0x7f, 'E', 'L', 'F'}, "esp32s3"); err == nil {
		t.Fatal("ELF accepted")
	}
}
