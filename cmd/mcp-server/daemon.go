package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// runDaemon runs as a background daemon: HTTP on both TCP and unix socket,
// no stdio. Shuts down after all driver proxies disconnect (with grace period).
func runDaemon(bundle *serverBundle) {
	socketPath := bundle.pol.SocketPath()
	pidFile := bundle.pol.PIDFile()
	graceSecs := bundle.pol.DaemonGracePeriodSeconds()

	if err := writePIDFile(pidFile); err != nil {
		bundle.logger.Fatalf("Daemon PID file: %v", err)
	}
	defer removePIDFile(pidFile)

	if err := removeStaleSocket(socketPath); err != nil {
		bundle.logger.Fatalf("Daemon socket cleanup: %v", err)
	}

	baseURL, handler, httpShutdown := setupAndServeHTTP(bundle)
	bundle.logger.Printf("Daemon: TCP server ready at %s", baseURL)

	unixLn, err := net.Listen("unix", socketPath)
	if err != nil {
		bundle.logger.Fatalf("Daemon unix socket: %v", err)
	}
	if err := os.Chmod(socketPath, 0700); err != nil {
		bundle.logger.Printf("Warning: chmod socket: %v", err)
	}

	// Track driver connections via unix socket connection lifecycle.
	// All unix socket connections come from proxy processes; TCP is for workers.
	tracker := newDriverTracker(time.Duration(graceSecs)*time.Second, bundle.logger)
	trackingLn := &connTrackingListener{
		Listener:     unixLn,
		onConnect:    tracker.driverConnected,
		onDisconnect: tracker.driverDisconnected,
	}

	unixServer := &http.Server{Handler: handler}
	go func() {
		if err := unixServer.Serve(trackingLn); err != http.ErrServerClosed {
			bundle.logger.Printf("Unix socket server error: %v", err)
		}
	}()
	bundle.logger.Printf("Daemon: unix socket ready at %s", socketPath)

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		unixServer.Shutdown(shutdownCtx)
		os.Remove(socketPath)
	}()

	bundle.logger.Println("Daemon: running (waiting for driver connections)")

	select {
	case <-tracker.done():
		bundle.logger.Println("Daemon: all drivers disconnected, shutting down")
	case <-tracker.ctx.Done():
		bundle.logger.Println("Daemon: context cancelled, shutting down")
	}

	httpShutdown()
	bundle.cleanup()
	bundle.logger.Println("Daemon stopped")
}

// connTrackingListener wraps a net.Listener and calls hooks on connect/disconnect.
type connTrackingListener struct {
	net.Listener
	onConnect    func()
	onDisconnect func()
}

func (l *connTrackingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.onConnect()
	return &trackedConn{Conn: conn, onClose: l.onDisconnect}, nil
}

type trackedConn struct {
	net.Conn
	onClose   func()
	closeOnce sync.Once
}

func (c *trackedConn) Close() error {
	c.closeOnce.Do(c.onClose)
	return c.Conn.Close()
}

// driverTracker counts connected driver proxies and triggers shutdown when
// the last one disconnects after a grace period.
type driverTracker struct {
	count      atomic.Int64
	grace      time.Duration
	logger     interface{ Printf(string, ...any) }
	ctx        context.Context
	cancel     context.CancelFunc
	doneCh     chan struct{}
	doneOnce   sync.Once
	graceTimer *time.Timer
	mu         sync.Mutex
}

func newDriverTracker(grace time.Duration, logger interface{ Printf(string, ...any) }) *driverTracker {
	ctx, cancel := context.WithCancel(context.Background())
	return &driverTracker{
		grace:  grace,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
		doneCh: make(chan struct{}),
	}
}

func (dt *driverTracker) driverConnected() {
	n := dt.count.Add(1)
	dt.logger.Printf("Daemon: driver connection opened (count=%d)", n)
	dt.mu.Lock()
	if dt.graceTimer != nil {
		dt.graceTimer.Stop()
		dt.graceTimer = nil
		dt.logger.Printf("Daemon: grace period cancelled")
	}
	dt.mu.Unlock()
}

func (dt *driverTracker) driverDisconnected() {
	n := dt.count.Add(-1)
	dt.logger.Printf("Daemon: driver connection closed (count=%d)", n)
	if n <= 0 {
		dt.mu.Lock()
		if dt.graceTimer == nil {
			dt.graceTimer = time.AfterFunc(dt.grace, func() {
				dt.logger.Printf("Daemon: grace period expired, signaling shutdown")
				dt.doneOnce.Do(func() { close(dt.doneCh) })
			})
			dt.logger.Printf("Daemon: grace period started (%s)", dt.grace)
		}
		dt.mu.Unlock()
	}
}

func (dt *driverTracker) done() <-chan struct{} {
	return dt.doneCh
}

// --- PID file management ---

func writePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create pid dir: %w", err)
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func removePIDFile(path string) {
	os.Remove(path)
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func isPIDAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func removeStaleSocket(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(path)
}

// --- Daemon detection and startup ---

func isDaemonRunning(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// startDaemonProcess starts the daemon as a detached subprocess.
// Uses a lockfile to prevent races when multiple proxies start simultaneously.
func startDaemonProcess(socketPath, pidFile string, logger interface{ Printf(string, ...any) }) error {
	lockPath := socketPath + ".lock"
	lock, err := acquireDaemonLock(lockPath)
	if err != nil {
		logger.Printf("Another process is starting daemon, waiting...")
		return waitForSocket(socketPath, 10*time.Second)
	}
	defer releaseDaemonLock(lock, lockPath)

	if isDaemonRunning(socketPath) {
		return nil
	}

	if pid, err := readPIDFile(pidFile); err == nil && !isPIDAlive(pid) {
		removePIDFile(pidFile)
		removeStaleSocket(socketPath)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.Command(exe, "--daemon")
	cmd.Env = os.Environ()
	cmd.Dir = os.Getenv("PWD")
	if cmd.Dir == "" {
		cmd.Dir, _ = os.Getwd()
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	logger.Printf("Daemon started (pid=%d)", cmd.Process.Pid)
	cmd.Process.Release()

	return waitForSocket(socketPath, 10*time.Second)
}

func waitForSocket(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	interval := 50 * time.Millisecond
	for time.Now().Before(deadline) {
		if isDaemonRunning(socketPath) {
			return nil
		}
		time.Sleep(interval)
		if interval < 500*time.Millisecond {
			interval = interval * 2
		}
	}
	return fmt.Errorf("daemon did not start within %s", timeout)
}

func acquireDaemonLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(f, "%d", os.Getpid())
	return f, nil
}

func releaseDaemonLock(f *os.File, path string) {
	f.Close()
	os.Remove(path)
}
