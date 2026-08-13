package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/syncservice"
)

const githubAPI = "https://api.github.com/repos/kuchmenko/workspace"
const githubDownloads = "https://github.com/kuchmenko/workspace/releases/download"

func Serve(ctx context.Context, input io.Reader, output io.Writer, stateDir, binaryPath string) error {
	if os.Geteuid() != 0 {
		return errors.New("updater requires root")
	}
	request, backupPath, err := decodeRequest(input, stateDir, binaryPath)
	if err != nil {
		return err
	}
	return apply(ctx, output, stateDir, binaryPath, backupPath, request.Version)
}

func decodeRequest(input io.Reader, stateDir, binaryPath string) (syncservice.UpdaterRequest, string, error) {
	var request syncservice.UpdaterRequest
	decoder := json.NewDecoder(io.LimitReader(input, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, "", err
	}
	backupPath, err := validateRequest(request, stateDir, binaryPath)
	if decoder.Decode(&struct{}{}) != io.EOF || err != nil {
		return request, "", errors.New("invalid updater request")
	}
	return request, backupPath, nil
}

func apply(ctx context.Context, output io.Writer, stateDir, binaryPath, backupPath, version string) error {
	if version == "latest" {
		var err error
		version, err = resolveLatest(ctx, githubAPI)
		if err != nil {
			return err
		}
	}
	binary, err := download(ctx, githubDownloads, version, runtime.GOARCH)
	if err != nil {
		return err
	}
	rollback := binaryPath + ".rollback"
	if err = replace(binaryPath, rollback, binary); err != nil {
		return err
	}
	if err = json.NewEncoder(output).Encode(syncservice.UpgradeResponse{Accepted: true, Version: version}); err != nil {
		restore(binaryPath, rollback)
		return err
	}
	if file, ok := output.(*os.File); ok {
		_ = file.Sync()
	}
	time.Sleep(100 * time.Millisecond)
	if err = systemctl("restart", "ws-sync.service"); err == nil {
		healthCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = pollActive(healthCtx, time.Second, 5*time.Second, serviceActive)
		cancel()
	}
	if err == nil {
		return nil
	}
	return errors.Join(err, rollbackUpgrade(binaryPath, rollback, stateDir, backupPath))
}

func validateRequest(request syncservice.UpdaterRequest, stateDir, binaryPath string) (string, error) {
	if !syncservice.ValidUpgradeVersion(request.Version) || !filepath.IsAbs(stateDir) || !filepath.IsAbs(binaryPath) || !filepath.IsAbs(request.BackupPath) {
		return "", errors.New("invalid updater request")
	}
	return validateBackupLocation(stateDir, request.BackupPath)
}

func validateBackupLocation(stateDir, backupPath string) (string, error) {
	state, err := filepath.EvalSymlinks(filepath.Clean(stateDir))
	if err != nil {
		return "", err
	}
	backup, err := filepath.EvalSymlinks(filepath.Clean(backupPath))
	if err != nil {
		return "", err
	}
	name := filepath.Base(backup)
	if filepath.Dir(backup) != filepath.Join(state, "backups") || !strings.HasPrefix(name, "service-") || !strings.HasSuffix(name, ".db") {
		return "", errors.New("backup is outside the state directory")
	}
	info, err := os.Stat(backup)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("backup is not a regular file")
	}
	return backup, nil
}

func serviceActive() error {
	return systemctl("is-active", "--quiet", "ws-sync.service")
}

func pollActive(ctx context.Context, interval, stableFor time.Duration, check func() error) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var stableSince time.Time
	for {
		if check() == nil {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= stableFor {
				return nil
			}
		} else {
			stableSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return errors.New("service did not remain active")
		case <-ticker.C:
		}
	}
}

func rollbackUpgrade(binaryPath, rollback, stateDir, backupPath string) error {
	stopErr := systemctl("stop", "ws-sync.service")
	binaryErr := restore(binaryPath, rollback)
	databaseErr := restoreDatabase(stateDir, backupPath)
	if err := errors.Join(stopErr, binaryErr, databaseErr); err != nil {
		return err
	}
	if err := systemctl("restart", "ws-sync.service"); err != nil {
		return err
	}
	return serviceActive()
}

func restoreDatabase(stateDir, backupPath string) error {
	validated, err := validateBackupPath(stateDir, backupPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(validated)
	if err != nil {
		return err
	}
	database := filepath.Join(stateDir, "service.db")
	if err = atomicReplace(database, data, 0o600); err != nil {
		return err
	}
	if err = os.Remove(database + "-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.Remove(database + "-shm"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(stateDir)
}

func validateBackupPath(stateDir, backupPath string) (string, error) {
	return validateRequest(syncservice.UpdaterRequest{Version: "latest", BackupPath: backupPath}, stateDir, filepath.Join(stateDir, "ws"))
}

func resolveLatest(ctx context.Context, api string) (string, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, api+"/releases/latest", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release: %s", response.Status)
	}
	var result struct {
		Tag string `json:"tag_name"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result) != nil || !syncservice.ValidUpgradeVersion(result.Tag) || result.Tag == "latest" {
		return "", errors.New("invalid latest release")
	}
	return result.Tag, nil
}

func download(ctx context.Context, base, version, arch string) ([]byte, error) {
	if arch != "amd64" && arch != "arm64" {
		return nil, errors.New("unsupported architecture")
	}
	asset := "ws-linux-" + arch + ".tar.gz"
	archive, err := fetch(ctx, base+"/"+version+"/"+asset, 128<<20)
	if err != nil {
		return nil, err
	}
	checksums, err := fetch(ctx, base+"/"+version+"/checksums.txt", 1<<20)
	if err != nil {
		return nil, err
	}
	want := checksumFor(checksums, asset)
	if want == "" {
		return nil, errors.New("asset checksum missing")
	}
	sum := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return nil, errors.New("asset checksum mismatch")
	}
	return extract(archive, "ws-linux-"+arch)
}

func fetch(ctx context.Context, address string, limit int64) ([]byte, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", address, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("download too large")
	}
	return data, err
}

func checksumFor(data []byte, asset string) string {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset && len(fields[0]) == 64 {
			return fields[0]
		}
	}
	return ""
}

func extract(data []byte, expected string) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var binary []byte
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		if header.Name != expected || header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != header.Name || binary != nil {
			return nil, errors.New("archive contains unexpected entry")
		}
		binary, err = io.ReadAll(io.LimitReader(reader, 128<<20))
		if err != nil {
			return nil, err
		}
	}
	if len(binary) == 0 {
		return nil, errors.New("expected binary missing")
	}
	return binary, nil
}

func replace(path, rollback string, data []byte) error {
	if current, err := os.ReadFile(path); err == nil {
		if err = atomicReplace(rollback, current, 0o755); err != nil {
			return err
		}
	}
	return atomicReplace(path, data, 0o755)
}

func atomicReplace(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".ws-update-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func restore(path, rollback string) error {
	data, err := os.ReadFile(rollback)
	if err != nil {
		return err
	}
	return replace(path, rollback+".failed", data)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func systemctl(args ...string) error {
	output, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
