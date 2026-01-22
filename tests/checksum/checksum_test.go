package checksum

import (
	"bytes"
	"testing"

	Checksum "github.com/JupiterMetaLabs/JMDN-FastSync/internal/checksum"
)

func TestChecksumCreate_CRC32(t *testing.T) {
	cs := &Checksum.Checksum{}
	data := []byte("hello world")

	t.Logf("[CRC32] Input: %q", string(data))

	got, err := cs.Create(data, Checksum.VersionCRC32)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	t.Logf("[CRC32] Got checksum (len=%d): %x", len(got), got)

	if len(got) != 4 {
		t.Fatalf("CRC32 checksum length = %d, want 4", len(got))
	}

	got2, err := cs.Create(data, Checksum.VersionCRC32)
	if err != nil {
		t.Fatalf("Create returned error (second call): %v", err)
	}
	t.Logf("[CRC32] Got checksum again: %x", got2)

	if !bytes.Equal(got, got2) {
		t.Fatalf("CRC32 not deterministic: %x != %x", got, got2)
	}
	t.Logf("[CRC32] Deterministic ✅")
}

func TestChecksumCreate_SHA256(t *testing.T) {
	cs := &Checksum.Checksum{}
	data := []byte("hello world")

	t.Logf("[SHA256] Input: %q", string(data))

	got, err := cs.Create(data, Checksum.VersionSHA256)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	t.Logf("[SHA256] Got checksum (len=%d): %x", len(got), got)

	if len(got) != 32 {
		t.Fatalf("SHA256 checksum length = %d, want 32", len(got))
	}

	got2, err := cs.Create(data, Checksum.VersionSHA256)
	if err != nil {
		t.Fatalf("Create returned error (second call): %v", err)
	}
	t.Logf("[SHA256] Got checksum again: %x", got2)

	if !bytes.Equal(got, got2) {
		t.Fatalf("SHA256 not deterministic: %x != %x", got, got2)
	}
	t.Logf("[SHA256] Deterministic ✅")
}

func TestChecksumCreate_NilData(t *testing.T) {
	cs := &Checksum.Checksum{}

	t.Logf("[NilData] Calling Create with nil input...")

	_, err := cs.Create(nil, Checksum.VersionCRC32)
	if err == nil {
		t.Fatalf("expected error for nil data, got nil")
	}
	t.Logf("[NilData] Got expected error ✅: %v", err)
}

func TestChecksumCreate_UnsupportedVersion(t *testing.T) {
	cs := &Checksum.Checksum{}
	data := []byte("x")

	t.Logf("[UnsupportedVersion] Input: %q", string(data))

	_, err := cs.Create(data, 999)
	if err == nil {
		t.Fatalf("expected error for unsupported version, got nil")
	}
	t.Logf("[UnsupportedVersion] Got expected error ✅: %v", err)
}

func TestChecksumVerify_Match(t *testing.T) {
	cs := &Checksum.Checksum{}
	data := []byte("hello world")

	t.Logf("[VerifyMatch] Input: %q", string(data))

	exp, err := cs.Create(data, Checksum.VersionSHA256)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	t.Logf("[VerifyMatch] Expected checksum: %x", exp)

	ok, err := cs.Verify(data, Checksum.VersionSHA256, exp)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	t.Logf("[VerifyMatch] Verify result: %v", ok)

	if !ok {
		t.Fatalf("Verify = false, want true")
	}
	t.Logf("[VerifyMatch] Match ✅")
}

func TestChecksumVerify_Mismatch(t *testing.T) {
	cs := &Checksum.Checksum{}
	data := []byte("hello world")
	bad := []byte("hello worle")

	t.Logf("[VerifyMismatch] Input good: %q", string(data))
	t.Logf("[VerifyMismatch] Input bad : %q", string(bad))

	exp, err := cs.Create(data, Checksum.VersionCRC32)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	t.Logf("[VerifyMismatch] Expected checksum (from good input): %x", exp)

	ok, err := cs.Verify(bad, Checksum.VersionCRC32, exp)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	t.Logf("[VerifyMismatch] Verify result: %v", ok)

	if ok {
		t.Fatalf("Verify = true, want false")
	}
	t.Logf("[VerifyMismatch] Mismatch detected ✅")
}

func TestChecksumVerify_LengthMismatch(t *testing.T) {
	cs := &Checksum.Checksum{}
	data := []byte("hello world")

	t.Logf("[LenMismatch] Input: %q", string(data))
	t.Logf("[LenMismatch] Providing wrong expected length (1 byte)...")

	ok, err := cs.Verify(data, Checksum.VersionCRC32, []byte{0x00})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	t.Logf("[LenMismatch] Verify result: %v", ok)

	if ok {
		t.Fatalf("Verify = true, want false (length mismatch)")
	}
	t.Logf("[LenMismatch] Length mismatch correctly failed ✅")
}

func TestChecksumVerify_CreateErrorPropagates(t *testing.T) {
	cs := &Checksum.Checksum{}
	data := []byte("hello world")

	t.Logf("[VerifyCreateErr] Input: %q", string(data))
	t.Logf("[VerifyCreateErr] Using unsupported version 999...")

	_, err := cs.Verify(data, 999, []byte{0x00})
	if err == nil {
		t.Fatalf("expected error for unsupported version, got nil")
	}
	t.Logf("[VerifyCreateErr] Got expected error ✅: %v", err)
}