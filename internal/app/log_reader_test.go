package app

import (
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func TestLogTailKeepsNewestBeyondByteLimit(t *testing.T) {
	for _, tty := range []bool{false, true} {
		text := strings.Repeat("older line with diagnostic detail\n", 12000) + "newest-marker\n"
		raw := []byte(text)
		if !tty {
			raw = logFrame(1, text)
		}
		got, err := decodeContainerLogs(raw, tty, 300)
		if err != nil || !got.Truncated || len(got.Lines) == 0 || got.Lines[len(got.Lines)-1].Text != "newest-marker" {
			t.Fatalf("newest tail missing (tty=%v): %v", tty, err)
		}
	}
}
func TestLogStreamBoundsAndMalformedFrames(t *testing.T) {
	raw := append(logFrame(1, strings.Repeat("x", maxLogBytes*2)+"\n"), logFrame(2, "last stderr\n")...)
	got, err := decodeContainerLogs(raw, false, 2000)
	if err != nil || !got.Truncated || len(got.Lines) == 0 || got.Lines[len(got.Lines)-1].Text != "last stderr" {
		t.Fatal("bounded stream lost final stderr", err)
	}
	size := 0
	for _, line := range got.Lines {
		size += len(line.Text) + len(line.Time)
	}
	if size > maxLogBytes {
		t.Fatal("retained log bytes exceed bound", size)
	}
	var header [8]byte
	header[0] = 1
	binary.BigEndian.PutUint32(header[4:], maxLogReadBytes+1)
	if _, err := decodeContainerLogs(header[:], false, 300); err == nil {
		t.Fatal("oversize frame accepted")
	}
	if _, err := decodeContainerLogStream(strings.NewReader(strings.Repeat("x", maxLogReadBytes+1)), true, 300); err == nil {
		t.Fatal("oversize TTY response accepted as a stale tail")
	}
	got, err = decodeContainerLogs([]byte(strings.Repeat("\xff", maxLogBytes)), true, 300)
	if err != nil || len(got.Lines) != 1 || len(got.Lines[0].Text) > maxLogBytes {
		t.Fatal("invalid UTF-8 exceeded retained bound", err)
	}
}

type shortLogReader struct{ io.Reader }

func (r shortLogReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:min(len(p), 1)]) }

type repeatingLogReader struct{ remaining int64 }

func (r *repeatingLogReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), r.remaining)
	for i := int64(0); i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= n
	return int(n), nil
}
func TestLogTailOrdersMixedUnterminatedStreams(t *testing.T) {
	for _, newest := range []string{"newest partial", "newest complete\n"} {
		raw := append(logFrame(2, "older partial"), logFrame(1, newest)...)
		got, err := decodeContainerLogs(raw, false, 1)
		if err != nil || len(got.Lines) != 1 || got.Lines[0].Text != strings.TrimSuffix(newest, "\n") {
			t.Fatal("older pending stream displaced newest line", err)
		}
	}
	raw := append(logFrame(1, "\xce"), logFrame(1, "\xb1\n")...)
	raw = append(raw, logFrame(2, "newest-marker\n")...)
	got, err := decodeContainerLogStream(shortLogReader{strings.NewReader(string(raw))}, false, 2)
	if err != nil || len(got.Lines) != 2 || got.Lines[0].Text != "\xce\xb1" || got.Lines[1].Text != "newest-marker" {
		t.Fatal("split header/payload/UTF-8 failed", err)
	}
}
func TestLogExactReadLimitStillShowsTail(t *testing.T) {
	reader := io.MultiReader(&repeatingLogReader{remaining: maxLogReadBytes - 4}, strings.NewReader("end\n"))
	got, err := decodeContainerLogStream(reader, true, 1)
	if err != nil || len(got.Lines) != 1 || !strings.HasSuffix(got.Lines[0].Text, "end") || len(got.Lines[0].Text) > maxLogBytes {
		t.Fatal("exact read boundary lost tail", err)
	}
}
