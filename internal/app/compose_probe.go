package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"
)

const maxComposeProbeBytes = 32 << 10

type composeProbeOutput struct {
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	overflow bool
}

func (w *composeProbeOutput) Write(raw []byte) (int, error) {
	if len(raw) > maxComposeProbeBytes-w.buffer.Len() {
		w.overflow = true
		w.cancel()
		return 0, errors.New("Compose probe output exceeds limit")
	}
	return w.buffer.Write(raw)
}

// Help/version probes never contact Docker, inherit credentials or return diagnostics.
func probeCompose(ctx context.Context, binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = []string{"DOCKER_CONFIG=/nonexistent", "HOME=/nonexistent", "PATH=/usr/bin:/bin"}
	command.WaitDelay = 100 * time.Millisecond
	output := composeProbeOutput{cancel: cancel}
	command.Stdout, command.Stderr = &output, io.Discard
	err := command.Run()
	if output.overflow {
		return nil, errors.New("Compose probe output exceeds 32 KiB")
	}
	if err != nil {
		return nil, errors.New("Compose capability probe failed")
	}
	return output.buffer.Bytes(), nil
}

func composeHelpSupports(raw []byte, flags ...string) bool {
	words := strings.Fields(string(raw))
	for _, flag := range flags {
		found := false
		for _, word := range words {
			if word == flag || strings.HasPrefix(word, flag+"=") {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
