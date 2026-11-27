package protocol

import "errors"

// Binary WebSocket frame opcodes.
const (
	OpTermOutput   byte = 0x01
	OpTermInput    byte = 0x02
	OpTermResize   byte = 0x03
	OpTermSnapshot byte = 0x04

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
