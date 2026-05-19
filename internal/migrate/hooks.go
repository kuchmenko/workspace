package migrate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func listActiveHooks(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".sample") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func copyHooks(srcDir, dstDir string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	var copied []string
	for _, name := range names {
		if err := copyFilePreservingMode(filepath.Join(srcDir, name), filepath.Join(dstDir, name)); err != nil {
			return copied, fmt.Errorf("copy hook %s: %w", name, err)
		}
		copied = append(copied, name)
	}
	return copied, nil
}

func copyFilePreservingMode(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
