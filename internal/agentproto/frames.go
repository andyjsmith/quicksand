package agentproto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Shell streams carry frames: a type byte, a big-endian uint32 payload
// length, and the payload.
type FrameType byte

const (
	// Client to agent.
	FrameStdin    FrameType = 1
	FrameStdinEOF FrameType = 2
	FrameResize   FrameType = 3 // JSON Resize
	// Agent to client.
	FrameStdout FrameType = 16
	FrameStderr FrameType = 17
	FrameExit   FrameType = 18 // JSON ShellExit; the last frame
)

// MaxFrame bounds frame payloads.
const MaxFrame = 1 << 20

// FrameWriter writes frames; it's safe for concurrent use.
type FrameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func NewFrameWriter(w io.Writer) *FrameWriter { return &FrameWriter{w: w} }

func (f *FrameWriter) Write(t FrameType, payload []byte) error {
	if len(payload) > MaxFrame {
		return errors.New("frame too large")
	}
	buf := make([]byte, 5+len(payload))
	buf[0] = byte(t)
	binary.BigEndian.PutUint32(buf[1:], uint32(len(payload)))
	copy(buf[5:], payload)
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.w.Write(buf)
	return err
}

func (f *FrameWriter) WriteJSON(t FrameType, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return f.Write(t, b)
}

// StreamWriter returns an io.Writer sending everything as frames of type t.
func (f *FrameWriter) StreamWriter(t FrameType) io.Writer { return frameStream{f, t} }

type frameStream struct {
	f *FrameWriter
	t FrameType
}

func (s frameStream) Write(p []byte) (int, error) {
	for off := 0; off < len(p); off += 32 << 10 {
		if err := s.f.Write(s.t, p[off:min(len(p), off+32<<10)]); err != nil {
			return off, err
		}
	}
	return len(p), nil
}

// ReadFrame reads one frame.
func ReadFrame(r io.Reader) (FrameType, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > MaxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes is too large", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return FrameType(hdr[0]), payload, nil
}
