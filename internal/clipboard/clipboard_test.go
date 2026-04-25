package clipboard

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestDetect_UnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		t.Skip("Detect returns ErrUnavailable only on unsupported platforms; this GOOS is supported")
	}
	_, _, err := Detect()
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("want ErrUnavailable, got %v", err)
	}
}

func TestRead_ContextCancelled(t *testing.T) {
	// Pre-cancelled context: regardless of whether a clipboard tool
	// exists, detect() may succeed and runTool is invoked — it must
	// surface ctx.Err(). If no tool → ErrUnavailable. Either outcome
	// is acceptable; unsupported platforms test ErrUnavailable above.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Read(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context or missing tool")
	}
	// Two valid outcomes: tool missing → ErrUnavailable, tool present → ctx err.
	if !errors.Is(err, ErrUnavailable) && !errors.Is(err, context.Canceled) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRead_DeadlineExceeded(t *testing.T) {
	// Very short deadline. Same two-outcome contract as above: tool
	// missing → ErrUnavailable, tool present but slow → context err.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(10 * time.Millisecond) // ensure deadline is exceeded

	_, err := Read(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
}

// fakeReader lets us exercise the Reader interface without touching the
// real clipboard — useful for consumers of this package (the `ws add`
// gather path) to wire a test double.
type fakeReader struct {
	val string
	err error
}

func (f fakeReader) Read(_ context.Context) (string, error) { return f.val, f.err }

func TestReaderInterface_CanSwapDefault(t *testing.T) {
	orig := DefaultReader
	t.Cleanup(func() { DefaultReader = orig })

	DefaultReader = fakeReader{val: "git@github.com:foo/bar.git"}
	got, err := Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "git@github.com:foo/bar.git" {
		t.Errorf("got %q", got)
	}
}
