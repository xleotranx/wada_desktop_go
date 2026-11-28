package protocol

import (
	"encoding/binary"
	"errors"
)

// Binary WebSocket frame opcodes.
const (
	OpTermOutput   byte = 0x01
	OpTermInput    byte = 0x02
	OpTermResize   byte = 0x03
	OpTermSnapshot byte = 0x04
	// OpTermExit (server → client, empty payload) ends a channel: the
	// terminal exited and the server released the channel after its output.
	OpTermExit byte = 0x05

	OpFileBegin byte = 0x10
	OpFileChunk byte = 0x11
	OpFileEnd   byte = 0x12
)

// FileTransferChunkSize is the standard chunk size for binary file transfers.
const FileTransferChunkSize = 256 << 10

var errInvalidBinaryFrame = errors.New("protocol: invalid binary frame")

// EncodeBinaryFrame encodes a binary stream frame: [1B op][1B channel][payload].
func EncodeBinaryFrame(op byte, channel uint8, payload []byte) []byte {
	frame := make([]byte, 2+len(payload))
	frame[0] = op
	frame[1] = channel
	copy(frame[2:], payload)
	return frame
}

// DecodeBinaryFrame decodes a binary stream frame encoded by EncodeBinaryFrame.
func DecodeBinaryFrame(frame []byte) (op byte, channel uint8, payload []byte, err error) {
	if len(frame) < 2 {
		return 0, 0, nil, errInvalidBinaryFrame
	}
	return frame[0], frame[1], frame[2:], nil
}

// EncodeTermResize encodes terminal cols/rows as big-endian uint16 pairs.
func EncodeTermResize(cols, rows int) []byte {
	var buf [4]byte
	if cols < 0 {
		cols = 0
	}
	if rows < 0 {
		rows = 0
	}
	binary.BigEndian.PutUint16(buf[0:2], uint16(min(cols, 0xffff)))
	binary.BigEndian.PutUint16(buf[2:4], uint16(min(rows, 0xffff)))
	return buf[:]
}

// DecodeTermResize decodes [2B cols][2B rows] from an OpTermResize payload.
func DecodeTermResize(payload []byte) (cols, rows int, err error) {
	if len(payload) < 4 {
		return 0, 0, errInvalidBinaryFrame
	}
	return int(binary.BigEndian.Uint16(payload[0:2])), int(binary.BigEndian.Uint16(payload[2:4])), nil
}
