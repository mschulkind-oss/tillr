package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
	"time"
)

const (
	eventStreamPreludeLen    = 12
	eventStreamTrailerLen    = 4
	eventStreamMinTotalLen   = eventStreamPreludeLen + eventStreamTrailerLen
	eventStreamMaxTotalLen   = 16 * 1024 * 1024 // 16 MiB AWS EventStream limit
	eventStreamMaxHeadersLen = 128 * 1024       // 128 KiB AWS EventStream limit
)

// AWS EventStream header value types.
const (
	EventStreamHeaderBoolTrue  uint8 = 0
	EventStreamHeaderBoolFalse uint8 = 1
	EventStreamHeaderByte      uint8 = 2
	EventStreamHeaderInt16     uint8 = 3
	EventStreamHeaderInt32     uint8 = 4
	EventStreamHeaderInt64     uint8 = 5
	EventStreamHeaderBytes     uint8 = 6
	EventStreamHeaderString    uint8 = 7
	EventStreamHeaderTimestamp uint8 = 8
	EventStreamHeaderUUID      uint8 = 9
)

// EventStreamHeader represents a single typed header in an EventStream frame.
type EventStreamHeader struct {
	Name  string
	Type  uint8
	Value any
}

// EventStreamMessage represents one decoded AWS application/vnd.amazon.eventstream frame.
type EventStreamMessage struct {
	Headers    map[string]string
	RawHeaders []EventStreamHeader
	Payload    []byte
}

// EncodeEventStreamMessage encodes string headers and payload into a binary
// AWS EventStream frame with prelude and message IEEE CRC32 checksums.
func EncodeEventStreamMessage(headers map[string]string, payload []byte) ([]byte, error) {
	raw := make([]EventStreamHeader, 0, len(headers))
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		raw = append(raw, EventStreamHeader{
			Name:  k,
			Type:  EventStreamHeaderString,
			Value: headers[k],
		})
	}
	return EncodeEventStreamRaw(raw, payload)
}

// EncodeEventStreamRaw encodes arbitrary typed EventStream headers and payload
// into a binary AWS EventStream frame.
func EncodeEventStreamRaw(headers []EventStreamHeader, payload []byte) ([]byte, error) {
	var hdrBuf bytes.Buffer
	for _, h := range headers {
		if len(h.Name) == 0 || len(h.Name) > 255 {
			return nil, fmt.Errorf("eventstream: invalid header name length %d", len(h.Name))
		}
		hdrBuf.WriteByte(byte(len(h.Name)))
		hdrBuf.WriteString(h.Name)
		hdrBuf.WriteByte(h.Type)

		switch h.Type {
		case EventStreamHeaderBoolTrue, EventStreamHeaderBoolFalse:
			// No value bytes.
		case EventStreamHeaderByte:
			v, ok := h.Value.(int8)
			if !ok {
				return nil, fmt.Errorf("eventstream: header %q expected int8", h.Name)
			}
			hdrBuf.WriteByte(byte(v))
		case EventStreamHeaderInt16:
			v, ok := h.Value.(int16)
			if !ok {
				return nil, fmt.Errorf("eventstream: header %q expected int16", h.Name)
			}
			var tmp [2]byte
			binary.BigEndian.PutUint16(tmp[:], uint16(v))
			hdrBuf.Write(tmp[:])
		case EventStreamHeaderInt32:
			v, ok := h.Value.(int32)
			if !ok {
				return nil, fmt.Errorf("eventstream: header %q expected int32", h.Name)
			}
			var tmp [4]byte
			binary.BigEndian.PutUint32(tmp[:], uint32(v))
			hdrBuf.Write(tmp[:])
		case EventStreamHeaderInt64:
			v, ok := h.Value.(int64)
			if !ok {
				return nil, fmt.Errorf("eventstream: header %q expected int64", h.Name)
			}
			var tmp [8]byte
			binary.BigEndian.PutUint64(tmp[:], uint64(v))
			hdrBuf.Write(tmp[:])
		case EventStreamHeaderBytes:
			v, ok := h.Value.([]byte)
			if !ok {
				return nil, fmt.Errorf("eventstream: header %q expected []byte", h.Name)
			}
			if len(v) > 65535 {
				return nil, fmt.Errorf("eventstream: header %q bytes too long (%d)", h.Name, len(v))
			}
			var tmp [2]byte
			binary.BigEndian.PutUint16(tmp[:], uint16(len(v)))
			hdrBuf.Write(tmp[:])
			hdrBuf.Write(v)
		case EventStreamHeaderString:
			v, ok := h.Value.(string)
			if !ok {
				return nil, fmt.Errorf("eventstream: header %q expected string", h.Name)
			}
			if len(v) > 65535 {
				return nil, fmt.Errorf("eventstream: header %q string too long (%d)", h.Name, len(v))
			}
			var tmp [2]byte
			binary.BigEndian.PutUint16(tmp[:], uint16(len(v)))
			hdrBuf.Write(tmp[:])
			hdrBuf.WriteString(v)
		case EventStreamHeaderTimestamp:
			var ms int64
			switch v := h.Value.(type) {
			case time.Time:
				ms = v.UnixMilli()
			case int64:
				ms = v
			default:
				return nil, fmt.Errorf("eventstream: header %q expected time.Time or int64", h.Name)
			}
			var tmp [8]byte
			binary.BigEndian.PutUint64(tmp[:], uint64(ms))
			hdrBuf.Write(tmp[:])
		case EventStreamHeaderUUID:
			v, ok := h.Value.([16]byte)
			if !ok {
				return nil, fmt.Errorf("eventstream: header %q expected [16]byte", h.Name)
			}
			hdrBuf.Write(v[:])
		default:
			return nil, fmt.Errorf("eventstream: unsupported header type %d", h.Type)
		}
	}

	headersLen := hdrBuf.Len()
	if headersLen > eventStreamMaxHeadersLen {
		return nil, fmt.Errorf("eventstream: headers length %d exceeds max %d", headersLen, eventStreamMaxHeadersLen)
	}
	totalLen := eventStreamMinTotalLen + headersLen + len(payload)
	if totalLen > eventStreamMaxTotalLen {
		return nil, fmt.Errorf("eventstream: total length %d exceeds max %d", totalLen, eventStreamMaxTotalLen)
	}

	frame := make([]byte, totalLen)
	binary.BigEndian.PutUint32(frame[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(frame[4:8], uint32(headersLen))
	preludeCRC := crc32.ChecksumIEEE(frame[0:8])
	binary.BigEndian.PutUint32(frame[8:12], preludeCRC)

	copy(frame[12:12+headersLen], hdrBuf.Bytes())
	copy(frame[12+headersLen:totalLen-4], payload)

	msgCRC := crc32.ChecksumIEEE(frame[:totalLen-4])
	binary.BigEndian.PutUint32(frame[totalLen-4:totalLen], msgCRC)
	return frame, nil
}

// EventStreamDecoder reads binary EventStream frames from an io.Reader,
// handling arbitrary read chunk splits.
type EventStreamDecoder struct {
	r            io.Reader
	maxTotalSize int
}

// NewEventStreamDecoder returns a streaming decoder reading from r.
func NewEventStreamDecoder(r io.Reader) *EventStreamDecoder {
	return &EventStreamDecoder{
		r:            r,
		maxTotalSize: eventStreamMaxTotalLen,
	}
}

// Next reads and validates the next EventStreamMessage from the stream.
// At a clean frame boundary with no more bytes, it returns (nil, io.EOF).
func (d *EventStreamDecoder) Next() (*EventStreamMessage, error) {
	if d == nil || d.r == nil {
		return nil, errors.New("eventstream: nil decoder or reader")
	}

	var prelude [eventStreamPreludeLen]byte
	if _, err := io.ReadFull(d.r, prelude[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("eventstream: reading prelude: %w", err)
	}

	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])

	if actualPreludeCRC := crc32.ChecksumIEEE(prelude[0:8]); actualPreludeCRC != preludeCRC {
		return nil, fmt.Errorf("eventstream: prelude CRC mismatch: got 0x%08x, want 0x%08x", actualPreludeCRC, preludeCRC)
	}
	if totalLen < eventStreamMinTotalLen {
		return nil, fmt.Errorf("eventstream: total length %d below minimum %d", totalLen, eventStreamMinTotalLen)
	}
	if int(totalLen) > d.maxTotalSize {
		return nil, fmt.Errorf("eventstream: total length %d exceeds maximum %d", totalLen, d.maxTotalSize)
	}
	if headersLen > eventStreamMaxHeadersLen {
		return nil, fmt.Errorf("eventstream: headers length %d exceeds maximum %d", headersLen, eventStreamMaxHeadersLen)
	}
	if headersLen > totalLen-eventStreamMinTotalLen {
		return nil, fmt.Errorf("eventstream: headers length %d exceeds frame body length %d", headersLen, totalLen-eventStreamMinTotalLen)
	}

	restLen := int(totalLen) - eventStreamPreludeLen
	rest := make([]byte, restLen)
	if _, err := io.ReadFull(d.r, rest); err != nil {
		return nil, fmt.Errorf("eventstream: truncated frame body: %w", err)
	}

	msgCRCExpected := binary.BigEndian.Uint32(rest[restLen-4:])
	h := crc32.NewIEEE()
	h.Write(prelude[:])
	h.Write(rest[:restLen-4])
	if actualMsgCRC := h.Sum32(); actualMsgCRC != msgCRCExpected {
		return nil, fmt.Errorf("eventstream: message CRC mismatch: got 0x%08x, want 0x%08x", actualMsgCRC, msgCRCExpected)
	}

	hdrBytes := rest[:headersLen]
	payloadBytes := make([]byte, restLen-4-int(headersLen))
	copy(payloadBytes, rest[headersLen:restLen-4])

	strHeaders, rawHeaders, err := decodeEventStreamHeaders(hdrBytes)
	if err != nil {
		return nil, err
	}

	return &EventStreamMessage{
		Headers:    strHeaders,
		RawHeaders: rawHeaders,
		Payload:    payloadBytes,
	}, nil
}

func decodeEventStreamHeaders(b []byte) (map[string]string, []EventStreamHeader, error) {
	strHeaders := make(map[string]string)
	var rawHeaders []EventStreamHeader

	pos := 0
	for pos < len(b) {
		nameLen := int(b[pos])
		pos++
		if nameLen == 0 {
			return nil, nil, errors.New("eventstream: empty header name")
		}
		if pos+nameLen+1 > len(b) {
			return nil, nil, errors.New("eventstream: truncated header name or type")
		}
		name := string(b[pos : pos+nameLen])
		pos += nameLen
		valType := b[pos]
		pos++

		var val any
		switch valType {
		case EventStreamHeaderBoolTrue:
			val = true
		case EventStreamHeaderBoolFalse:
			val = false
		case EventStreamHeaderByte:
			if pos+1 > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated byte header %q", name)
			}
			val = int8(b[pos])
			pos++
		case EventStreamHeaderInt16:
			if pos+2 > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated int16 header %q", name)
			}
			val = int16(binary.BigEndian.Uint16(b[pos : pos+2]))
			pos += 2
		case EventStreamHeaderInt32:
			if pos+4 > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated int32 header %q", name)
			}
			val = int32(binary.BigEndian.Uint32(b[pos : pos+4]))
			pos += 4
		case EventStreamHeaderInt64:
			if pos+8 > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated int64 header %q", name)
			}
			val = int64(binary.BigEndian.Uint64(b[pos : pos+8]))
			pos += 8
		case EventStreamHeaderBytes:
			if pos+2 > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated bytes header length %q", name)
			}
			n := int(binary.BigEndian.Uint16(b[pos : pos+2]))
			pos += 2
			if pos+n > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated bytes header %q", name)
			}
			cp := make([]byte, n)
			copy(cp, b[pos:pos+n])
			val = cp
			pos += n
		case EventStreamHeaderString:
			if pos+2 > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated string header length %q", name)
			}
			n := int(binary.BigEndian.Uint16(b[pos : pos+2]))
			pos += 2
			if pos+n > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated string header %q", name)
			}
			s := string(b[pos : pos+n])
			val = s
			strHeaders[name] = s
			pos += n
		case EventStreamHeaderTimestamp:
			if pos+8 > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated timestamp header %q", name)
			}
			ms := int64(binary.BigEndian.Uint64(b[pos : pos+8]))
			val = time.UnixMilli(ms).UTC()
			pos += 8
		case EventStreamHeaderUUID:
			if pos+16 > len(b) {
				return nil, nil, fmt.Errorf("eventstream: truncated uuid header %q", name)
			}
			var u [16]byte
			copy(u[:], b[pos:pos+16])
			val = u
			pos += 16
		default:
			return nil, nil, fmt.Errorf("eventstream: unknown header type %d for %q", valType, name)
		}

		rawHeaders = append(rawHeaders, EventStreamHeader{
			Name:  name,
			Type:  valType,
			Value: val,
		})
	}
	return strHeaders, rawHeaders, nil
}
