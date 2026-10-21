// Package protocol implements Chrome Native Messaging framing: a 4-byte
// little-endian length followed by that many bytes of UTF-8 JSON.
//
// Nothing else may ever be written to stdout: one stray byte breaks the
// framing. Logs go to a file (see package logs).
//
// Unlike Python, Go does not translate line endings on Windows stdin and
// stdout, so no binary-mode switch is needed there.
package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"leadin-helper/internal/version"
)

const (
	// MaxIncoming is Chrome's limit for a browser → helper message.
	MaxIncoming = 4 << 20
	// MaxOutgoing is Chrome's limit for a helper → browser message.
	MaxOutgoing = 1 << 20
)

// ErrInvalid reports a message that is truncated, too large, not JSON or not
// a JSON object.
var ErrInvalid = errors.New("invalid native message")

// ErrTooLarge reports an outgoing message above MaxOutgoing, which Chrome
// would drop.
var ErrTooLarge = errors.New("outgoing message above 1 MB")

// ReadMessage reads one message. It returns io.EOF when the stream ends
// cleanly before the header, and ErrInvalid for any malformed message.
//
// Numbers are decoded as json.Number, so a run_id of 7.5 or 1e3 can be told
// apart from an integer.
func ReadMessage(r io.Reader) (map[string]any, error) {
	var header [4]byte
	n, err := io.ReadFull(r, header[:])
	if n == 0 && errors.Is(err, io.EOF) {
		return nil, io.EOF
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size > MaxIncoming {
		return nil, ErrInvalid
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrInvalid
		}
		return nil, err
	}
	// encoding/json silently replaces invalid UTF-8: reject it instead.
	if !utf8.Valid(body) {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalid
	}
	// Anything after the first JSON value makes the message invalid.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalid
	}
	message, ok := value.(map[string]any)
	if !ok {
		return nil, ErrInvalid
	}
	return message, nil
}

// WriteMessage writes one message, header and body in a single Write. It
// refuses messages above MaxOutgoing.
func WriteMessage(w io.Writer, message any) error {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(message); err != nil {
		return err
	}
	// Encode adds a trailing newline.
	payload := bytes.TrimSuffix(body.Bytes(), []byte("\n"))
	if len(payload) > MaxOutgoing {
		return ErrTooLarge
	}
	frame := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)
	_, err := w.Write(frame)
	return err
}

// ErrorReply is the reply to a request that cannot be served.
func ErrorReply(code string) map[string]any {
	return map[string]any{"type": "error", "code": code}
}

// RunFailed is the run_failed event with its error code. fields carries the
// known versions and, when claude ended with a result event, the run metrics:
// an interrupted run has still consumed usage.
func RunFailed(code string, fields map[string]any) map[string]any {
	event := map[string]any{}
	for key, value := range fields {
		event[key] = value
	}
	event["type"] = "run_failed"
	event["code"] = code
	event["helper_version"] = version.Version
	return event
}
