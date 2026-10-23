package app

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
)

// Refuse oversized responses instead of presenting a stale prefix as the tail.
const maxLogReadBytes = 32 << 20

func decodeContainerLogs(raw []byte, tty bool, tail int) (ContainerLogs, error) {
	return decodeContainerLogStream(bytes.NewReader(raw), tty, tail)
}

func decodeContainerLogStream(input io.Reader, tty bool, tail int) (ContainerLogs, error) {
	result := ContainerLogs{Lines: []LogLine{}, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if tail < 1 || tail > 2000 {
		return result, errors.New("Log tail must be between 1 and 2000 lines")
	}
	reader := &io.LimitedReader{R: input, N: maxLogReadBytes + 1}
	pending := map[string]string{}
	pendingOrder := map[string]uint64{}
	var sequence uint64
	orders := []uint64{}
	retained := 0
	appendLine := func(stream, text string, order uint64) {
		line := LogLine{Stream: stream, Text: strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\t' {
				return -1
			}
			return r
		}, text)}
		if stamp, text, ok := strings.Cut(line.Text, " "); ok {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				line.Time, line.Text = stamp, text
			}
		}
		budget := maxLogBytes - len(line.Time)
		if len(line.Text) > budget {
			line.Text = strings.ToValidUTF8(line.Text[len(line.Text)-budget:], "")
			result.Truncated = true
		}
		result.Lines = append(result.Lines, line)
		orders = append(orders, order)
		for i := len(orders) - 1; i > 0 && orders[i] < orders[i-1]; i-- {
			orders[i], orders[i-1] = orders[i-1], orders[i]
			result.Lines[i], result.Lines[i-1] = result.Lines[i-1], result.Lines[i]
		}
		retained += len(line.Text) + len(line.Time)
		for len(result.Lines) > 1 && (len(result.Lines) > tail || retained > maxLogBytes) {
			retained -= len(result.Lines[0].Text) + len(result.Lines[0].Time)
			result.Lines[0] = LogLine{}
			result.Lines = result.Lines[1:]
			orders = orders[1:]
			result.Truncated = true
		}
	}
	consume := func(stream string, data []byte) {
		for len(data) > 0 {
			end := bytes.IndexByte(data, '\n')
			piece := data
			if end >= 0 {
				piece = data[:end]
			}
			text := pending[stream] + string(piece)
			if len(text) > maxLogBytes {
				text = text[len(text)-maxLogBytes:]
				result.Truncated = true
			}
			sequence++
			pending[stream], pendingOrder[stream] = text, sequence
			if end < 0 {
				return
			}
			appendLine(stream, text, sequence)
			pending[stream] = ""
			data = data[end+1:]
		}
	}
	oversized := func() error {
		return errors.New("Container log response exceeds 32 MiB; reduce the tail or time range")
	}
	chunk := make([]byte, 32<<10)
	if tty {
		for {
			n, err := reader.Read(chunk)
			if reader.N == 0 {
				return result, oversized()
			}
			if n > 0 {
				consume("console", chunk[:n])
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return result, errors.New("Container log retrieval was interrupted; retry the view")
			}
		}
	} else {
		var header [8]byte
		for {
			n, err := io.ReadFull(reader, header[:])
			if reader.N == 0 {
				return result, oversized()
			}
			if err == io.EOF && n == 0 {
				break
			}
			if err != nil {
				return result, errors.New("Docker returned an incomplete log frame")
			}
			if (header[0] != 1 && header[0] != 2) || header[1] != 0 || header[2] != 0 || header[3] != 0 {
				return result, errors.New("Docker returned an unsupported log format")
			}
			remaining := int64(binary.BigEndian.Uint32(header[4:]))
			if remaining > maxLogReadBytes {
				return result, oversized()
			}
			stream := "stdout"
			if header[0] == 2 {
				stream = "stderr"
			}
			for remaining > 0 {
				n, err := io.ReadFull(reader, chunk[:min(int64(len(chunk)), remaining)])
				if reader.N == 0 {
					return result, oversized()
				}
				if err != nil {
					return result, errors.New("Docker returned an incomplete log frame")
				}
				consume(stream, chunk[:n])
				remaining -= int64(n)
			}
		}
	}
	for _, stream := range []string{"stdout", "stderr", "console"} {
		if pending[stream] != "" {
			appendLine(stream, pending[stream], pendingOrder[stream])
		}
	}
	return result, nil
}
