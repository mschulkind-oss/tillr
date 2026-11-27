package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"reflect"
	"testing"
	"time"
)

type chunkedReader struct {
	data      []byte
	chunkSize int
	pos       int
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := c.chunkSize
	if n > len(p) {
		n = len(p)
	}
	if c.pos+n > len(c.data) {
		n = len(c.data) - c.pos
	}
	copy(p[:n], c.data[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}

func TestEventStreamRoundTrip(t *testing.T) {
	frames := []struct {
		headers map[string]string
		payload []byte
	}{
		{
			headers: map[string]string{
				":message-type": "event",
				":event-type":   "messageStart",
				":content-type": "application/json",
			},
			payload: []byte(`{"role":"assistant"}`),
		},
		{
			headers: map[string]string{
				":message-type": "event",
				":event-type":   "contentBlockDelta",
				":content-type": "application/json",
			},
			payload: []byte(`{"contentBlockIndex":0,"delta":{"text":"hello"}}`),
		},
		{
			headers: map[string]string{
				":message-type":   "exception",
				":exception-type": "throttlingException",
				":content-type":   "application/json",
			},
			payload: []byte(`{"message":"Rate exceeded"}`),
		},
	}

	var wire bytes.Buffer
	for _, f := range frames {
		b, err := EncodeEventStreamMessage(f.headers, f.payload)
		if err != nil {
			t.Fatalf("EncodeEventStreamMessage: %v", err)
		}
		wire.Write(b)
	}

	dec := NewEventStreamDecoder(bytes.NewReader(wire.Bytes()))
	for i, want := range frames {
		got, err := dec.Next()
		if err != nil {
			t.Fatalf("frame %d Next: %v", i, err)
		}
		if !reflect.DeepEqual(got.Headers, want.headers) {
			t.Fatalf("frame %d headers = %v, want %v", i, got.Headers, want.headers)
		}
		if !bytes.Equal(got.Payload, want.payload) {
			t.Fatalf("frame %d payload = %q, want %q", i, got.Payload, want.payload)
		}
	}
	if _, err := dec.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after last frame, got %v", err)
	}
}

func TestEventStreamAllHeaderTypesRoundTrip(t *testing.T) {
	ts := time.Date(2026, 11, 27, 10, 20, 30, 123000000, time.UTC)
	uuidVal := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	raw := []EventStreamHeader{
		{Name: "b-true", Type: EventStreamHeaderBoolTrue, Value: true},
		{Name: "b-false", Type: EventStreamHeaderBoolFalse, Value: false},
		{Name: "i8", Type: EventStreamHeaderByte, Value: int8(-7)},
		{Name: "i16", Type: EventStreamHeaderInt16, Value: int16(-1234)},
		{Name: "i32", Type: EventStreamHeaderInt32, Value: int32(123456)},
		{Name: "i64", Type: EventStreamHeaderInt64, Value: int64(-9876543210)},
		{Name: "bin", Type: EventStreamHeaderBytes, Value: []byte{0xde, 0xad, 0xbe, 0xef}},
		{Name: ":event-type", Type: EventStreamHeaderString, Value: "metadata"},
		{Name: "ts", Type: EventStreamHeaderTimestamp, Value: ts},
		{Name: "uid", Type: EventStreamHeaderUUID, Value: uuidVal},
	}

	frame, err := EncodeEventStreamRaw(raw, []byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("EncodeEventStreamRaw: %v", err)
	}

	dec := NewEventStreamDecoder(bytes.NewReader(frame))
	msg, err := dec.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if msg.Headers[":event-type"] != "metadata" {
		t.Fatalf(":event-type = %q, want metadata", msg.Headers[":event-type"])
	}
	if !reflect.DeepEqual(msg.RawHeaders, raw) {
		t.Fatalf("RawHeaders mismatch:\n got: %#v\nwant: %#v", msg.RawHeaders, raw)
	}
}

func TestEventStreamChunkSplitDecoding(t *testing.T) {
	f1, err := EncodeEventStreamMessage(map[string]string{":event-type": "messageStart"}, []byte(`{"role":"assistant"}`))
	if err != nil {
		t.Fatalf("encode f1: %v", err)
	}
	f2, err := EncodeEventStreamMessage(map[string]string{":event-type": "contentBlockDelta"}, []byte(`{"delta":{"text":"chunked streaming test"}}`))
	if err != nil {
		t.Fatalf("encode f2: %v", err)
	}
	f3, err := EncodeEventStreamMessage(map[string]string{":event-type": "messageStop"}, []byte(`{"stopReason":"end_turn"}`))
	if err != nil {
		t.Fatalf("encode f3: %v", err)
	}
	stream := append(append(append([]byte{}, f1...), f2...), f3...)

	for _, chunkSize := range []int{1, 3, 7, 13} {
		dec := NewEventStreamDecoder(&chunkedReader{data: stream, chunkSize: chunkSize})
		var events []string
		for {
			msg, err := dec.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("chunkSize=%d Next: %v", chunkSize, err)
			}
			events = append(events, msg.Headers[":event-type"])
		}
		want := []string{"messageStart", "contentBlockDelta", "messageStop"}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("chunkSize=%d events = %v, want %v", chunkSize, events, want)
		}
	}
}

func TestEventStreamValidationFailures(t *testing.T) {
	valid, err := EncodeEventStreamMessage(map[string]string{":event-type": "ping"}, []byte(`{}`))
	if err != nil {
		t.Fatalf("EncodeEventStreamMessage: %v", err)
	}

	t.Run("prelude CRC mismatch", func(t *testing.T) {
		bad := append([]byte(nil), valid...)
		bad[8] ^= 0xFF
		dec := NewEventStreamDecoder(bytes.NewReader(bad))
		if _, err := dec.Next(); err == nil {
			t.Fatal("expected prelude CRC error")
		}
	})

	t.Run("message CRC mismatch", func(t *testing.T) {
		bad := append([]byte(nil), valid...)
		bad[len(bad)-1] ^= 0xFF
		dec := NewEventStreamDecoder(bytes.NewReader(bad))
		if _, err := dec.Next(); err == nil {
			t.Fatal("expected message CRC error")
		}
	})

	t.Run("truncated prelude", func(t *testing.T) {
		dec := NewEventStreamDecoder(bytes.NewReader(valid[:5]))
		if _, err := dec.Next(); err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("expected truncated prelude error, got %v", err)
		}
	})

	t.Run("truncated body", func(t *testing.T) {
		dec := NewEventStreamDecoder(bytes.NewReader(valid[:len(valid)-3]))
		if _, err := dec.Next(); err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("expected truncated body error, got %v", err)
		}
	})

	t.Run("oversized total length", func(t *testing.T) {
		var hdr [12]byte
		binary.BigEndian.PutUint32(hdr[0:4], eventStreamMaxTotalLen+1)
		binary.BigEndian.PutUint32(hdr[4:8], 0)
		binary.BigEndian.PutUint32(hdr[8:12], crc32.ChecksumIEEE(hdr[0:8]))
		dec := NewEventStreamDecoder(bytes.NewReader(hdr[:]))
		if _, err := dec.Next(); err == nil {
			t.Fatal("expected oversized length error")
		}
	})

	t.Run("undersized total length", func(t *testing.T) {
		var hdr [12]byte
		binary.BigEndian.PutUint32(hdr[0:4], 12)
		binary.BigEndian.PutUint32(hdr[4:8], 0)
		binary.BigEndian.PutUint32(hdr[8:12], crc32.ChecksumIEEE(hdr[0:8]))
		dec := NewEventStreamDecoder(bytes.NewReader(hdr[:]))
		if _, err := dec.Next(); err == nil {
			t.Fatal("expected undersized length error")
		}
	})

	t.Run("headers length exceeds body", func(t *testing.T) {
		var hdr [12]byte
		binary.BigEndian.PutUint32(hdr[0:4], 20)
		binary.BigEndian.PutUint32(hdr[4:8], 8) // max allowed for total=20 is 4
		binary.BigEndian.PutUint32(hdr[8:12], crc32.ChecksumIEEE(hdr[0:8]))
		dec := NewEventStreamDecoder(bytes.NewReader(hdr[:]))
		if _, err := dec.Next(); err == nil {
			t.Fatal("expected headers length error")
		}
	})
}
