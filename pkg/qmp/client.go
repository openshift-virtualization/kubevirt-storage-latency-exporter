package qmp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"
)

const qmpFlag = 0

type qmpCommand struct {
	Execute   string `json:"execute"`
	Arguments any    `json:"arguments,omitempty"`
}

type qmpResponse struct {
	Return json.RawMessage `json:"return"`
	Error  *struct {
		Class string `json:"class"`
		Desc  string `json:"desc"`
	} `json:"error,omitempty"`
}

type Client struct {
	mu     sync.Mutex
	conn   net.Conn
	lv     *libvirt.Libvirt
	domain libvirt.Domain
	closed bool
}

func Dial(virtqemudSockPath, domainName string) (*Client, error) {
	conn, err := net.Dial("unix", virtqemudSockPath)
	if err != nil {
		return nil, fmt.Errorf("dialing virtqemud at %s: %w", virtqemudSockPath, err)
	}

	dialer := dialers.NewAlreadyConnected(conn)
	lv := libvirt.NewWithDialer(dialer)

	if err := lv.ConnectToURI(libvirt.QEMUSession); err != nil {
		conn.Close()
		return nil, fmt.Errorf("connecting to libvirt session: %w", err)
	}

	domain, err := lv.DomainLookupByName(domainName)
	if err != nil {
		lv.Disconnect()
		conn.Close()
		return nil, fmt.Errorf("looking up domain %s: %w", domainName, err)
	}

	return &Client{
		conn:   conn,
		lv:     lv,
		domain: domain,
	}, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.lv.Disconnect()
	return c.conn.Close()
}

func (c *Client) QueryBlockStats(ctx context.Context) (*BlockStatsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("client is closed")
	}

	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{})
	}

	result, err := c.execQMP("query-blockstats", nil)
	if err != nil {
		return nil, err
	}

	var devices []BlockDevice
	if err := json.Unmarshal(result, &devices); err != nil {
		return nil, fmt.Errorf("parsing blockstats response: %w", err)
	}

	return &BlockStatsResponse{Return: devices}, nil
}

func (c *Client) EnableHistogram(ctx context.Context, deviceID string, boundariesNs []int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("client is closed")
	}

	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{})
	}

	args := map[string]any{
		"id":         deviceID,
		"boundaries": boundariesNs,
	}
	_, err := c.execQMP("block-latency-histogram-set", args)
	return err
}

func (c *Client) AgentCommand(ctx context.Context, cmd string, timeoutSec int32) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", fmt.Errorf("client is closed")
	}

	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{})
	}

	result, err := c.lv.QEMUDomainAgentCommand(c.domain, cmd, timeoutSec, 0)
	if err != nil {
		return "", err
	}
	if len(result) == 0 {
		return "", fmt.Errorf("empty response from guest agent")
	}
	return result[0], nil
}

func (c *Client) QueryVirtio(ctx context.Context) ([]VirtioDevice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("client is closed")
	}

	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{})
	}

	result, err := c.execQMP("x-query-virtio", nil)
	if err != nil {
		return nil, err
	}

	var devices []VirtioDevice
	if err := json.Unmarshal(result, &devices); err != nil {
		return nil, fmt.Errorf("parsing x-query-virtio response: %w", err)
	}

	return devices, nil
}

func (c *Client) QueryVirtioStatus(ctx context.Context, path string) (*VirtioStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("client is closed")
	}

	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{})
	}

	args := map[string]any{"path": path}
	result, err := c.execQMP("x-query-virtio-status", args)
	if err != nil {
		return nil, err
	}

	var status VirtioStatus
	if err := json.Unmarshal(result, &status); err != nil {
		return nil, fmt.Errorf("parsing x-query-virtio-status response: %w", err)
	}

	return &status, nil
}

func (c *Client) QueryVirtioQueueStatus(ctx context.Context, path string, queue int) (*VirtQueueStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("client is closed")
	}

	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{})
	}

	args := map[string]any{"path": path, "queue": queue}
	result, err := c.execQMP("x-query-virtio-queue-status", args)
	if err != nil {
		return nil, err
	}

	var status VirtQueueStatus
	if err := json.Unmarshal(result, &status); err != nil {
		return nil, fmt.Errorf("parsing x-query-virtio-queue-status response: %w", err)
	}

	return &status, nil
}

// DomainGetXMLDesc returns the libvirt domain XML for the connected domain.
func (c *Client) DomainGetXMLDesc() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", fmt.Errorf("client is closed")
	}
	return c.lv.DomainGetXMLDesc(c.domain, 0)
}

func (c *Client) execQMP(command string, args any) (json.RawMessage, error) {
	cmd := qmpCommand{Execute: command, Arguments: args}
	cmdJSON, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshaling QMP command: %w", err)
	}

	result, err := c.lv.QEMUDomainMonitorCommand(c.domain, string(cmdJSON), uint32(qmpFlag))
	if err != nil {
		return nil, fmt.Errorf("QMP %s: %w", command, err)
	}

	var resp qmpResponse
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return nil, fmt.Errorf("parsing QMP response for %s: %w", command, err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("QMP %s error: %s: %s", command, resp.Error.Class, resp.Error.Desc)
	}

	return resp.Return, nil
}
