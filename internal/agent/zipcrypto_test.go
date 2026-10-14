package agent

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// zipCryptoReader decrypts, written independently of the writer from
// APPNOTE.TXT 6.1.
type zipCryptoReader struct {
	r    io.Reader
	k    [3]uint32
	skip int
}

func (z *zipCryptoReader) upd(b byte) {
	crc := func(c uint32, b byte) uint32 {
		c ^= uint32(b)
		for range 8 {
			if c&1 != 0 {
				c = c>>1 ^ 0xEDB88320
			} else {
				c >>= 1
			}
		}
		return c
	}
	z.k[0] = crc(z.k[0], b)
	z.k[1] = (z.k[1]+(z.k[0]&0xff))*134775813 + 1
	z.k[2] = crc(z.k[2], byte(z.k[1]>>24))
}

func (z *zipCryptoReader) dec(b byte) byte {
	t := uint16(z.k[2] | 2)
	p := b ^ byte((uint32(t)*uint32(t^1))>>8)
	z.upd(p)
	return p
}

func (z *zipCryptoReader) Read(p []byte) (int, error) {
	n, err := z.r.Read(p)
	for i := range n {
		p[i] = z.dec(p[i])
	}
	return n, err
}

var errBadPassword = errors.New("wrong password")

// openEncrypted reads a ZipCrypto archive, checking the password the way
// unzip tools do (the header's check byte).
func openEncrypted(t *testing.T, data []byte, password string) (map[string][]byte, error) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		if f.Mode().IsDir() {
			continue
		}
		if f.Flags&1 == 0 || f.Flags&8 == 0 || f.Method != zip.Deflate {
			t.Fatalf("%s: flags %#x method %d", f.Name, f.Flags, f.Method)
		}
		raw, err := f.OpenRaw()
		if err != nil {
			t.Fatal(err)
		}
		z := &zipCryptoReader{r: raw, k: [3]uint32{0x12345678, 0x23456789, 0x34567890}}
		for _, b := range []byte(password) {
			z.upd(b)
		}
		hdr := make([]byte, 12)
		if _, err := io.ReadFull(z, hdr); err != nil {
			t.Fatal(err)
		}
		if hdr[11] != byte(f.ModifiedTime>>8) {
			return nil, errBadPassword
		}
		b, err := io.ReadAll(flate.NewReader(z))
		if err != nil {
			return nil, err
		}
		out[f.Name] = b
	}
	return out, nil
}

func TestEncryptedZip(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 3<<20)
	rand.Read(big)
	files := map[string][]byte{
		"a.txt":         []byte("hello, quicksand\n"),
		"empty":         {},
		"sub/big.bin":   big,
		"sub/deep/x.md": bytes.Repeat([]byte("compressible "), 10000),
	}
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	if err := writeZip(&buf, dir, "infected"); err != nil {
		t.Fatal(err)
	}
	got, err := openEncrypted(t, buf.Bytes(), "infected")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		if !bytes.Equal(got[name], want) {
			t.Errorf("%s: got %d bytes, want %d", name, len(got[name]), len(want))
		}
	}
	if _, err := openEncrypted(t, buf.Bytes(), "wrong"); err == nil {
		t.Error("opened with the wrong password")
	}

	// A single file zips as one entry.
	buf.Reset()
	if err := writeZip(&buf, filepath.Join(dir, "a.txt"), "pw"); err != nil {
		t.Fatal(err)
	}
	if got, err := openEncrypted(t, buf.Bytes(), "pw"); err != nil || len(got) != 1 || string(got["a.txt"]) != "hello, quicksand\n" {
		t.Errorf("single file: %q, %v", got, err)
	}
}
