package daemonipc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	protocolVersion = 1
	maxMessageBytes = 4096
)

type Request struct {
	Version     int    `json:"version"`
	Action      string `json:"action"`
	WorkspaceID string `json:"workspace_id"`
}

type Response struct {
	Error string `json:"error,omitempty"`
}

type Server struct {
	listener *net.UnixListener
	path     string
	close    sync.Once
	closeErr error
}

func SocketPath(registryPath string) string {
	absolute, err := filepath.Abs(registryPath)
	if err != nil {
		absolute = filepath.Clean(registryPath)
	}
	digest := sha256.Sum256([]byte(absolute))
	return filepath.Join("/tmp", fmt.Sprintf("ws-%d", os.Getuid()), fmt.Sprintf("daemon-%x.sock", digest[:8]))
}

func Listen(registryPath string) (*Server, error) {
	path := SocketPath(registryPath)
	if err := ensureSocketDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return &Server{listener: listener, path: path}, nil
}

func ensureSocketDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("daemon socket directory is not a directory")
	}
	return os.Chmod(path, 0o700)
}

func removeStaleSocket(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	connection, err := net.DialTimeout("unix", path, 50*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		return errors.New("daemon is already running")
	}
	return os.Remove(path)
}

func (server *Server) Serve(ctx context.Context, wake func(string)) error {
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	for {
		connection, err := server.listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		server.handle(connection, wake)
	}
}

func (server *Server) handle(connection *net.UnixConn, wake func(string)) {
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	var request Request
	err := json.NewDecoder(io.LimitReader(connection, maxMessageBytes)).Decode(&request)
	if err == nil {
		err = validate(request)
	}
	if err == nil {
		wake(request.WorkspaceID)
	}
	response := Response{}
	if err != nil {
		response.Error = err.Error()
	}
	_ = json.NewEncoder(connection).Encode(response)
}

func (server *Server) Close() error {
	server.close.Do(func() {
		err := server.listener.Close()
		removeErr := os.Remove(server.path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		server.closeErr = errors.Join(err, removeErr)
	})
	return server.closeErr
}

func Wake(ctx context.Context, registryPath, workspaceID string) error {
	request := Request{Version: protocolVersion, Action: "workspace.wake", WorkspaceID: workspaceID}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", SocketPath(registryPath))
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err = json.NewEncoder(connection).Encode(request); err != nil {
		return err
	}
	var response Response
	if err = json.NewDecoder(io.LimitReader(connection, maxMessageBytes)).Decode(&response); err != nil {
		return err
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

func validate(request Request) error {
	if request.Version != protocolVersion {
		return fmt.Errorf("unsupported daemon protocol version %d", request.Version)
	}
	if request.Action != "workspace.wake" {
		return fmt.Errorf("unsupported daemon action %q", request.Action)
	}
	return nil
}
