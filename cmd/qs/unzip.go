package main

import (
	"bufio"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// extractZip extracts a zip archive into dir as it streams in, so nothing
// waits for (or stores) the whole archive. It reads the entries' local
// headers in order instead of the central directory at the end, which
// works for archives written front to back, as agents (Go's archive/zip)
// write them: deflated or stored entries, sizes in data descriptors.
func extractZip(r io.Reader, dir string, onFile func(name string)) error {
	br := &countingReader{r: bufio.NewReaderSize(r, 64<<10)}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for {
		var sig [4]byte
		if _, err := io.ReadFull(br, sig[:]); err != nil {
			return fmt.Errorf("the archive is truncated: %w", err)
		}
		switch binary.LittleEndian.Uint32(sig[:]) {
		case 0x04034b50: // local file header
		case 0x02014b50, 0x06054b50, 0x06064b50: // central directory: done
			_, err := io.Copy(io.Discard, br)
			return err
		default:
			return errors.New("not a zip archive this client can stream")
		}
		var h [26]byte
		if _, err := io.ReadFull(br, h[:]); err != nil {
			return err
		}
		le16 := func(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
		le32 := func(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
		flags, method := le16(h[2:]), le16(h[4:])
		crc, csize := le32(h[10:]), le32(h[14:])
		meta := make([]byte, int(le16(h[22:]))+int(le16(h[24:])))
		if _, err := io.ReadFull(br, meta); err != nil {
			return err
		}
		name, extra := string(meta[:le16(h[22:])]), meta[le16(h[22:]):]
		if flags&1 != 0 {
			return errors.New("the archive is encrypted")
		}
		rel := strings.TrimSuffix(name, "/")
		if !filepath.IsLocal(filepath.FromSlash(rel)) || strings.Contains(rel, `\`) {
			return fmt.Errorf("refusing to extract %q outside the destination", name)
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}

		start := br.n
		var data io.Reader
		var fr io.ReadCloser
		switch {
		case method == 8:
			// br is an io.ByteReader, so flate reads exactly the entry.
			fr = flate.NewReader(br)
			data = fr
		case method == 0 && flags&8 == 0:
			data = io.LimitReader(br, int64(csize))
		default:
			return fmt.Errorf("%s: unsupported zip method %d", name, method)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.Create(target)
		if err != nil {
			return err
		}
		sum := crc32.NewIEEE()
		usize, err := io.Copy(io.MultiWriter(f, sum), data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if fr != nil {
			fr.Close()
		}
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if flags&8 != 0 {
			// Data descriptor: signature, CRC, then sizes, 64-bit if
			// either is 4 GiB or more (archive/zip's rule).
			sizes := 8
			if br.n-start >= 0xFFFFFFFF || usize >= 0xFFFFFFFF {
				sizes = 16
			}
			d := make([]byte, 4+4+sizes)
			if _, err := io.ReadFull(br, d[:4]); err != nil {
				return err
			}
			if le32(d) != 0x08074b50 {
				// No signature: what was read is the CRC.
				copy(d[4:], d[:4])
				if _, err := io.ReadFull(br, d[8:]); err != nil {
					return err
				}
			} else if _, err := io.ReadFull(br, d[4:]); err != nil {
				return err
			}
			crc = le32(d[4:])
		}
		if sum.Sum32() != crc {
			return fmt.Errorf("%s: corrupt (checksum mismatch)", name)
		}
		if mtime, ok := extendedMtime(extra); ok {
			os.Chtimes(target, mtime, mtime)
		}
		if onFile != nil {
			onFile(rel)
		}
	}
}

// extendedMtime reads the modification time from an extended timestamp
// extra field (0x5455), which archive/zip writes.
func extendedMtime(extra []byte) (time.Time, bool) {
	for len(extra) >= 4 {
		id, size := binary.LittleEndian.Uint16(extra), int(binary.LittleEndian.Uint16(extra[2:]))
		if len(extra) < 4+size {
			break
		}
		if body := extra[4 : 4+size]; id == 0x5455 && size >= 5 && body[0]&1 != 0 {
			return time.Unix(int64(int32(binary.LittleEndian.Uint32(body[1:]))), 0), true
		}
		extra = extra[4+size:]
	}
	return time.Time{}, false
}

// countingReader counts what's read through it, and is an io.ByteReader so
// flate doesn't read ahead past the end of an entry.
type countingReader struct {
	r *bufio.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}
