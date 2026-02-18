package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// runProxy bridges Cursor's stdio JSON-RPC to the daemon's StreamableHTTP
// endpoint over a unix socket. Each proxy gets its own MCP session.
func runProxy(socketPath string, logger interface{ Printf(string, ...any) }) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
	}
	client := &http.Client{Transport: transport}

	p := &proxyBridge{
		client: client,
		logger: logger,
		stdout: os.Stdout,
	}

	// Read JSON-RPC from stdin and forward to daemon.
	// The first message should be "initialize" which establishes the session.
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		if err := p.forward(ctx, line); err != nil {
			logger.Printf("Proxy forward error: %v", err)
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Printf("Proxy stdin read error: %v", err)
		return err
	}

	// Send DELETE to clean up the session
	p.closeSession(ctx)

	logger.Printf("Proxy: stdin closed, exiting")
	return nil
}

// proxyBridge handles the stdio-to-HTTP translation for a single MCP session.
type proxyBridge struct {
	client    *http.Client
	logger    interface{ Printf(string, ...any) }
	stdout    io.Writer
	writeMu   sync.Mutex
	sessionID string
	sseCancel context.CancelFunc
}

const daemonBaseURL = "http://daemon/mcp"

// forward sends a JSON-RPC message to the daemon and relays the response to stdout.
func (p *proxyBridge) forward(ctx context.Context, jsonRPC []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daemonBaseURL, bytes.NewReader(jsonRPC))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if p.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", p.sessionID)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("daemon request: %w", err)
	}
	defer resp.Body.Close()

	// Capture session ID from the initialize response
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && p.sessionID == "" {
		p.sessionID = sid
		p.logger.Printf("Proxy: session established: %s", sid)
		// Start SSE notification listener now that we have a session
		p.startNotificationStream(ctx)
	}

	contentType := resp.Header.Get("Content-Type")

	switch {
	case strings.HasPrefix(contentType, "text/event-stream"):
		return p.relaySSE(resp.Body)
	case strings.HasPrefix(contentType, "application/json"):
		return p.relayJSON(resp.Body)
	default:
		// Accepted response with no body (e.g. 202 for notifications)
		if resp.StatusCode == http.StatusAccepted {
			return nil
		}
		body, _ := io.ReadAll(resp.Body)
		if len(body) > 0 {
			return p.writeStdout(body)
		}
		return nil
	}
}

// relayJSON reads a single JSON response and writes it to stdout.
func (p *proxyBridge) relayJSON(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read json response: %w", err)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	return p.writeStdout(data)
}

// relaySSE parses an SSE stream and writes each event's data as a JSON-RPC line to stdout.
func (p *proxyBridge) relaySSE(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if len(data) > 0 {
				if err := p.writeStdout([]byte(data)); err != nil {
					return err
				}
			}
		}
	}
	return scanner.Err()
}

// startNotificationStream opens a GET SSE connection to receive server-pushed notifications.
func (p *proxyBridge) startNotificationStream(ctx context.Context) {
	if p.sessionID == "" {
		return
	}

	sseCtx, sseCancel := context.WithCancel(ctx)
	p.sseCancel = sseCancel

	go func() {
		defer sseCancel()

		req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, daemonBaseURL, nil)
		if err != nil {
			p.logger.Printf("Proxy SSE: create request: %v", err)
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Mcp-Session-Id", p.sessionID)

		resp, err := p.client.Do(req)
		if err != nil {
			if sseCtx.Err() != nil {
				return
			}
			p.logger.Printf("Proxy SSE: connect: %v", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			p.logger.Printf("Proxy SSE: unexpected status %d", resp.StatusCode)
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if sseCtx.Err() != nil {
				return
			}
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if len(data) > 0 {
					if err := p.writeStdout([]byte(data)); err != nil {
						p.logger.Printf("Proxy SSE: write error: %v", err)
						return
					}
				}
			}
		}
	}()
}

// closeSession sends a DELETE request to terminate the MCP session.
func (p *proxyBridge) closeSession(ctx context.Context) {
	if p.sseCancel != nil {
		p.sseCancel()
	}
	if p.sessionID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, daemonBaseURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", p.sessionID)
	resp, err := p.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// writeStdout writes a JSON-RPC line to stdout (thread-safe, newline-terminated).
func (p *proxyBridge) writeStdout(data []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.stdout.Write(data); err != nil {
		return err
	}
	_, err := p.stdout.Write([]byte("\n"))
	return err
}
