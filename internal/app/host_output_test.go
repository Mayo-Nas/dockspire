package app

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestHostOutputBoundCannotBeBypassedByCopy(t *testing.T) {
	for _, size := range []int{4 << 20, (4 << 20) + 1} {
		ctx, cancel := context.WithCancel(context.Background())
		output := boundedOutput{cancel: cancel}
		// LimitedReader hides strings.Reader.WriteTo, exposing a promoted ReadFrom bypass if reintroduced.
		source := &io.LimitedReader{R: strings.NewReader(strings.Repeat("x", size)), N: int64(size)}
		copied, err := io.Copy(&output, source)
		if copied > 4<<20 || output.buffer.Len() > 4<<20 {
			t.Fatal("host output exceeded memory cap")
		}
		if (size > 4<<20) != (err != nil) {
			t.Fatal("host output boundary not enforced", size, err)
		}
		if (size > 4<<20) != (ctx.Err() == context.Canceled) {
			t.Fatal("output cap did not cancel only overflowing producer")
		}
		cancel()
	}
}
