package agent

import (
	"archive/zip"
	"compress/flate"
	"crypto/rand"
	"hash/crc32"
	"io"
)

// zipCryptoWriter encrypts a zip entry's (compressed) data with the
// traditional PKWARE scheme, "ZipCrypto" (APPNOTE.TXT 6.1). It's what every
// unzip tool, Windows Explorer included, can open, but it's weak: a known
// piece of plaintext is enough to break it.
type zipCryptoWriter struct {
	w    io.Writer
	keys [3]uint32
	// header is the 12-byte encryption header, sent with the first data:
	// archive/zip creates the compressor before writing the entry's header.
	header []byte
	buf    []byte
}

// newZipCryptoWriter starts an entry; check is the header's last byte,
// which tools compare to verify the password: with sizes in a data
// descriptor, the high byte of the entry's DOS modification time.
func newZipCryptoWriter(w io.Writer, password string, check byte) *zipCryptoWriter {
	z := &zipCryptoWriter{w: w, keys: [3]uint32{0x12345678, 0x23456789, 0x34567890}}
	for _, b := range []byte(password) {
		z.update(b)
	}
	z.header = make([]byte, 12)
	rand.Read(z.header[:11])
	z.header[11] = check
	return z
}

func crc32Byte(crc uint32, b byte) uint32 { return crc32.IEEETable[byte(crc)^b] ^ crc>>8 }

func (z *zipCryptoWriter) update(b byte) {
	z.keys[0] = crc32Byte(z.keys[0], b)
	z.keys[1] = (z.keys[1]+z.keys[0]&0xff)*134775813 + 1
	z.keys[2] = crc32Byte(z.keys[2], byte(z.keys[1]>>24))
}

func (z *zipCryptoWriter) encrypt(dst, src []byte) {
	for i, p := range src {
		t := z.keys[2] | 2
		dst[i] = p ^ byte((t*(t^1))>>8)
		z.update(p)
	}
}

func (z *zipCryptoWriter) writeHeader() error {
	if z.header == nil {
		return nil
	}
	h := z.header
	z.header = nil
	z.encrypt(h, h)
	_, err := z.w.Write(h)
	return err
}

func (z *zipCryptoWriter) Write(p []byte) (int, error) {
	if err := z.writeHeader(); err != nil {
		return 0, err
	}
	if cap(z.buf) < len(p) {
		z.buf = make([]byte, len(p))
	}
	buf := z.buf[:len(p)]
	z.encrypt(buf, p)
	return z.w.Write(buf)
}

// encryptedDeflate is a compressor deflating into a zipCryptoWriter.
type encryptedDeflate struct {
	*flate.Writer
	enc *zipCryptoWriter
}

func (e encryptedDeflate) Close() error {
	if err := e.Writer.Close(); err != nil {
		return err
	}
	return e.enc.writeHeader()
}

// encryptEntries makes zw deflate and encrypt every entry created through
// createEncrypted with password.
func encryptEntries(zw *zip.Writer, password string) (createEncrypted func(*zip.FileHeader) (io.Writer, error)) {
	var current *zip.FileHeader
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		// CreateHeader has set the DOS time by the time it calls this.
		enc := newZipCryptoWriter(w, password, byte(current.ModifiedTime>>8))
		fw, err := flate.NewWriter(enc, flate.DefaultCompression)
		return encryptedDeflate{fw, enc}, err
	})
	return func(fh *zip.FileHeader) (io.Writer, error) {
		fh.Method = zip.Deflate
		fh.Flags |= 0x1 // encrypted
		current = fh
		return zw.CreateHeader(fh)
	}
}
