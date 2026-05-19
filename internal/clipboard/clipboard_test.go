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
		t.Skip("detect returns ErrUnavailable only on unsupported platforms; this GOOS is supported")
	}
	_, _, err := detect()
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("want ErrUnavailable, got %v", err)
	}
}

func TestRead_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := DefaultReader.Read(ctx)
	if err == nil {
		t.Fatal("expected error from canceled context or missing tool")
	}
	if !errors.Is(err, ErrUnavailable) && !errors.Is(err, context.Canceled) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRead_DeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(10 * time.Millisecond)

	_, err := DefaultReader.Read(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeReader struct {
	val string
	err error
}

func (f fakeReader) Read(_ context.Context) (string, error) { return f.val, f.err }

func TestReaderInterface_CanSwapDefault(t *testing.T) {
	orig := DefaultReader
	t.Cleanup(func() { DefaultReader = orig })

	DefaultReader = fakeReader{val: "git@github.com:foo/bar.git"}
	got, err := DefaultReader.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "git@github.com:foo/bar.git" {
		t.Errorf("got %q", got)
	}
}
