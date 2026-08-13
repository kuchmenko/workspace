package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/syncservice"
)

func archive(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return out.Bytes()
}

func TestValidateRequestRestrictsBackupToStateDirectory(t *testing.T) {
	stateDir := t.TempDir()
	backupDir := filepath.Join(stateDir, "backups")
	if err := os.Mkdir(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(backupDir, "service-20260808.db")
	if err := os.WriteFile(backup, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := syncservice.UpdaterRequest{Version: "v1.2.3", BackupPath: backup}
	if got, err := validateRequest(request, stateDir, "/usr/local/bin/ws"); err != nil || got != backup {
		t.Fatalf("validateRequest = %q, %v", got, err)
	}
	outside := filepath.Join(t.TempDir(), "service-20260808.db")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	request.BackupPath = outside
	if _, err := validateRequest(request, stateDir, "/usr/local/bin/ws"); err == nil {
		t.Fatal("accepted backup outside state directory")
	}
	request.BackupPath = backup
	request.Version = "v1.2.3; reboot"
	if _, err := validateRequest(request, stateDir, "/usr/local/bin/ws"); err == nil {
		t.Fatal("accepted invalid version")
	}
}

func TestRestoreDatabaseReplacesDatabaseAndRemovesSidecars(t *testing.T) {
	stateDir := t.TempDir()
	backupDir := filepath.Join(stateDir, "backups")
	if err := os.Mkdir(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(backupDir, "service-20260808.db")
	for path, body := range map[string]string{backup: "good", filepath.Join(stateDir, "service.db"): "bad", filepath.Join(stateDir, "service.db-wal"): "wal", filepath.Join(stateDir, "service.db-shm"): "shm"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := restoreDatabase(stateDir, backup); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "service.db"))
	if err != nil || string(data) != "good" {
		t.Fatalf("database = %q, %v", data, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(stateDir, "service.db") + suffix); !os.IsNotExist(err) {
			t.Fatalf("sidecar %s remains: %v", suffix, err)
		}
	}
}

func TestPollActiveRequiresContinuousStabilityAndTimesOut(t *testing.T) {
	var checks atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := pollActive(ctx, time.Millisecond, 5*time.Millisecond, func() error {
		if checks.Add(1) == 3 {
			return fmt.Errorf("inactive")
		}
		return nil
	})
	if err != nil || checks.Load() < 7 {
		t.Fatalf("pollActive checks=%d err=%v", checks.Load(), err)
	}
	timeout, stop := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer stop()
	if pollActive(timeout, time.Millisecond, time.Millisecond, func() error { return fmt.Errorf("inactive") }) == nil {
		t.Fatal("inactive service did not time out")
	}
}

func TestDownloadChecksumAndSafeArchive(t *testing.T) {
	asset := archive(t, "ws-linux-amd64", []byte("binary"))
	sum := sha256.Sum256(asset)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/checksums.txt" {
			fmt.Fprintf(w, "%x  ws-linux-amd64.tar.gz\n", sum)
			return
		}
		w.Write(asset)
	}))
	defer server.Close()
	got, err := download(context.Background(), server.URL, "v1", "amd64")
	if err != nil || string(got) != "binary" {
		t.Fatalf("download = %q, %v", got, err)
	}
	asset = archive(t, "../ws-linux-amd64", []byte("bad"))
	sum = sha256.Sum256(asset)
	if _, err = download(context.Background(), server.URL, "v1", "amd64"); err == nil {
		t.Fatal("accepted traversal archive")
	}
}
