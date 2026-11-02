package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/G-DPP/leadin-helper/internal/version"
)

func frame(body []byte) []byte {
	out := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(out, uint32(len(body)))
	copy(out[4:], body)
	return out
}

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMessage(&buf, map[string]any{"action": "get_info", "run_id": 7}); err != nil {
		t.Fatal(err)
	}
	message, err := ReadMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if message["action"] != "get_info" || message["run_id"] != json.Number("7") {
		t.Fatalf("unexpected message: %#v", message)
	}
}

func TestLengthIsLittleEndian(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMessage(&buf, map[string]any{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	body := `{"a":"b"}`
	if got := binary.LittleEndian.Uint32(data[:4]); got != uint32(len(body)) {
		t.Fatalf("length %d, want %d", got, len(body))
	}
	if !bytes.Equal(data[:4], []byte{byte(len(body)), 0, 0, 0}) {
		t.Fatalf("header %v is not little-endian", data[:4])
	}
	if string(data[4:]) != body {
		t.Fatalf("body %q, want %q", data[4:], body)
	}
}

func TestEmptyStreamIsEOF(t *testing.T) {
	if _, err := ReadMessage(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestIncompleteHeaderIsInvalid(t *testing.T) {
	if _, err := ReadMessage(bytes.NewReader([]byte{1, 0})); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestTruncatedBodyIsInvalid(t *testing.T) {
	data := frame([]byte(`{"action":"get_info"}`))
	if _, err := ReadMessage(bytes.NewReader(data[:len(data)-3])); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestInvalidJSONIsInvalid(t *testing.T) {
	for _, body := range []string{`{"action":`, `not json`, `{"a":1} {"b":2}`, "\"\xff\""} {
		if _, err := ReadMessage(bytes.NewReader(frame([]byte(body)))); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v, want ErrInvalid", body, err)
		}
	}
}

func TestArrayIsInvalid(t *testing.T) {
	for _, body := range []string{`[1,2]`, `null`, `"text"`, `3`} {
		if _, err := ReadMessage(bytes.NewReader(frame([]byte(body)))); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v, want ErrInvalid", body, err)
		}
	}
}

func TestIncomingAboveFourMegabytesIsInvalid(t *testing.T) {
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], MaxIncoming+1)
	// No body: the length alone must be enough to reject it.
	if _, err := ReadMessage(bytes.NewReader(header[:])); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestIncomingAtFourMegabytesIsAccepted(t *testing.T) {
	padding := strings.Repeat("x", MaxIncoming-len(`{"a":""}`))
	body := []byte(`{"a":"` + padding + `"}`)
	if _, err := ReadMessage(bytes.NewReader(frame(body))); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestOutgoingAboveOneMegabyteIsRefused(t *testing.T) {
	var buf bytes.Buffer
	err := WriteMessage(&buf, map[string]any{"a": strings.Repeat("x", MaxOutgoing)})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("%d bytes written, want none", buf.Len())
	}
}

func TestNonASCIIRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	text := "perché — 日本語 <&>"
	if err := WriteMessage(&buf, map[string]any{"text": text}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(text)) {
		t.Fatalf("text not written as UTF-8: %q", buf.Bytes())
	}
	message, err := ReadMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if message["text"] != text {
		t.Fatalf("text = %q, want %q", message["text"], text)
	}
}

func TestNumbersKeepTheirForm(t *testing.T) {
	message, err := ReadMessage(bytes.NewReader(frame([]byte(`{"a":7.5,"b":1e3}`))))
	if err != nil {
		t.Fatal(err)
	}
	if message["a"] != json.Number("7.5") || message["b"] != json.Number("1e3") {
		t.Fatalf("unexpected numbers: %#v", message)
	}
}

func TestRunFailedCarriesHelperVersion(t *testing.T) {
	event := RunFailed("unknown", map[string]any{"claude_version": "2.1.300", "type": "ignored"})
	want := map[string]any{
		"type":           "run_failed",
		"code":           "unknown",
		"helper_version": version.Version,
		"claude_version": "2.1.300",
	}
	if len(event) != len(want) {
		t.Fatalf("event = %#v", event)
	}
	for key, value := range want {
		if event[key] != value {
			t.Errorf("%s = %#v, want %#v", key, event[key], value)
		}
	}
}

func TestErrorReply(t *testing.T) {
	reply := ErrorReply("invalid_request")
	if reply["type"] != "error" || reply["code"] != "invalid_request" || len(reply) != 2 {
		t.Fatalf("reply = %#v", reply)
	}
}
