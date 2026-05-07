package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

type Client struct {
	path    string
	conn    net.Conn
	mu      sync.Mutex
	onEvent func(Event)
}

func NewClient(sockPath string) *Client {
	return &Client{path: sockPath}
}

func (c *Client) OnEvent(fn func(Event)) {
	c.mu.Lock()
	c.onEvent = fn
	c.mu.Unlock()
}

func (c *Client) Connect() error {
	conn, err := net.Dial("unix", c.path)
	if err != nil {
		return fmt.Errorf("ipc client dial: %w", err)
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	go c.readLoop(conn)
	return nil
}

func (c *Client) Send(cmd Command) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	return json.NewEncoder(conn).Encode(cmd)
}

func (c *Client) Close() {
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()
}

func (c *Client) readLoop(conn net.Conn) {
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		c.mu.Lock()
		cb := c.onEvent
		c.mu.Unlock()
		if cb != nil {
			cb(ev)
		}
	}
}
