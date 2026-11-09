package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

var ErrNotRunning = errors.New("the remote-control daemon is not running (start it with: systemctl --user start remote-control)")

func dial(socket string) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return nil, ErrNotRunning
	}
	return conn, nil
}

// Do sends one request and returns the daemon's answer. A failed request
// still returns the snapshots, with the error.
func Do(socket string, req Request) (Response, error) {
	conn, err := dial(socket)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("read daemon reply: %w", err)
	}
	if !resp.OK {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// Watch calls fn with every snapshot the daemon streams until the connection
// drops or fn returns an error.
func Watch(socket string, fn func(Response) error) error {
	conn, err := dial(socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "watch"}); err != nil {
		return err
	}
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			return err
		}
		if err := fn(resp); err != nil {
			return err
		}
	}
}
