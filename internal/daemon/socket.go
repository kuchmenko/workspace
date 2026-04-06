package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
)

type Request struct {
	Cmd       string `json:"cmd"`
	Workspace string `json:"workspace,omitempty"`
	Event     string `json:"event,omitempty"`
}

type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

type StatusData struct {
	Running    bool             `json:"running"`
	Workspaces []WorkspaceEntry `json:"workspaces"`
	PID        int              `json:"pid"`
}

func listenSocket(socketPath string) (net.Listener, error) {
	// Clean stale socket
	if _, err := os.Stat(socketPath); err == nil {
		os.Remove(socketPath)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", socketPath, err)
	}

	// Restrict permissions
	os.Chmod(socketPath, 0o600)
	return ln, nil
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeResponse(conn, Response{OK: false, Error: "invalid request"})
		return
	}

	switch req.Cmd {
	case "status":
		writeResponse(conn, Response{
			OK: true,
			Data: StatusData{
				Running:    true,
				Workspaces: d.config.Workspaces,
				PID:        os.Getpid(),
			},
		})

	case "notify":
		if req.Workspace == "" || req.Event == "" {
			writeResponse(conn, Response{OK: false, Error: "workspace and event required"})
			return
		}
		d.handleNotify(req.Workspace, req.Event)
		writeResponse(conn, Response{OK: true})

	case "stop":
		writeResponse(conn, Response{OK: true})
		d.Shutdown()

	default:
		writeResponse(conn, Response{OK: false, Error: fmt.Sprintf("unknown command: %s", req.Cmd)})
	}
}

func writeResponse(conn net.Conn, resp Response) {
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	conn.Write(data)
}
