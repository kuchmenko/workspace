package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type IPCClient struct {
	conn net.Conn
}

// Dial connects to the daemon socket.
func Dial() (*IPCClient, error) {
	socketPath, err := SocketPath()
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("daemon not running (connect %s: %w)", socketPath, err)
	}
	return &IPCClient{conn: conn}, nil
}

func (c *IPCClient) Close() {
	c.conn.Close()
}

func (c *IPCClient) send(req Request) (Response, error) {
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := c.conn.Write(data); err != nil {
		return Response{}, err
	}

	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := bufio.NewScanner(c.conn)
	if !scanner.Scan() {
		return Response{}, fmt.Errorf("no response from daemon")
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

// Status queries the daemon for its current state.
func (c *IPCClient) Status() (StatusData, error) {
	resp, err := c.send(Request{Cmd: "status"})
	if err != nil {
		return StatusData{}, err
	}
	if !resp.OK {
		return StatusData{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	// Re-marshal Data to decode into StatusData
	raw, _ := json.Marshal(resp.Data)
	var status StatusData
	json.Unmarshal(raw, &status)
	return status, nil
}

// Notify tells the daemon about a workspace event.
func (c *IPCClient) Notify(workspace, event string) error {
	resp, err := c.send(Request{Cmd: "notify", Workspace: workspace, Event: event})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// Stop tells the daemon to shut down.
func (c *IPCClient) Stop() error {
	resp, err := c.send(Request{Cmd: "stop"})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}
