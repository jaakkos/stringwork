package main

import (
	"log"
	"os"
	"testing"
	"time"
)

// testTrackerLogger returns a silent logger for tracker tests.
func testTrackerLogger() interface{ Printf(string, ...any) } {
	return log.New(os.Stderr, "[tracker-test] ", 0)
}

// TestDriverTracker_UnixConnection_StartsAndStopsGrace confirms that once an
// MCP session has connected, closing the last unix proxy connection starts
// grace shutdown when no MCP sessions remain.
func TestDriverTracker_UnixConnection_StartsAndStopsGrace(t *testing.T) {
	tr := newDriverTracker(50*time.Millisecond, testTrackerLogger())

	tr.mcpSessionConnected()
	tr.mcpSessionDisconnected()
	tr.driverConnected()
	tr.driverDisconnected()

	select {
	case <-tr.done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected grace period to fire after last unix driver disconnected")
	}
}

// TestDriverTracker_IdleStartup_DoesNotShutDownImmediately ensures that a
// freshly-created tracker with zero connections does NOT immediately consider
// itself "all drivers disconnected". The grace period should only start after
// at least one driver has actually connected and then disconnected — not on
// cold start. This guards the invariant that a daemon which is just waiting
// for its first driver stays alive indefinitely.
func TestDriverTracker_IdleStartup_DoesNotShutDownImmediately(t *testing.T) {
	tr := newDriverTracker(50*time.Millisecond, testTrackerLogger())

	select {
	case <-tr.done():
		t.Fatal("tracker fired done() immediately on cold start — grace timer must not start at count=0 before any driver has connected")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestDriverTracker_MCPSession_CountsAsDriver is the regression test for the
// bug that surfaced while writing the end-to-end lifecycle test:
//
// The previous implementation only counted unix-socket connections as
// "drivers". A Streamable HTTP client (e.g. an IDE configured to talk MCP
// directly over TCP, or the e2e test itself) would register an MCP session
// but never open a unix connection. The tracker's count stayed at zero; a
// brief unix probe (from `isDaemonRunning` during startup) was enough to
// flip the count from 0→1→0 and start the grace timer, even though a real
// MCP driver was actively using the daemon.
//
// The fix is to treat an MCP session as a driver connection too, so that
// the daemon stays alive as long as EITHER a unix proxy OR an MCP session
// is connected.
func TestDriverTracker_MCPSession_CountsAsDriver(t *testing.T) {
	tr := newDriverTracker(50*time.Millisecond, testTrackerLogger())

	// A Streamable HTTP driver opens an MCP session directly over TCP.
	tr.mcpSessionConnected()

	// A transient unix probe (simulating `isDaemonRunning`) opens and
	// closes a unix connection. This MUST NOT trip the grace timer,
	// because the MCP session is still active.
	tr.driverConnected()
	tr.driverDisconnected()

	select {
	case <-tr.done():
		t.Fatal("tracker fired grace period while an MCP session was still active — a TCP-direct driver must keep the daemon alive")
	case <-time.After(200 * time.Millisecond):
	}

	// Now close the MCP session: grace period should start.
	tr.mcpSessionDisconnected()
	select {
	case <-tr.done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected grace period to fire after the last MCP session closed")
	}
}

// TestDriverTracker_MixedDriversCoalesce verifies that unix and MCP counts
// accumulate correctly: the grace period only starts when BOTH drop to zero.
func TestDriverTracker_MixedDriversCoalesce(t *testing.T) {
	tr := newDriverTracker(50*time.Millisecond, testTrackerLogger())

	tr.driverConnected()     // unix=1, mcp=0
	tr.mcpSessionConnected() // unix=1, mcp=1
	tr.driverDisconnected()  // unix=0, mcp=1 — still alive via MCP

	select {
	case <-tr.done():
		t.Fatal("grace period started even though an MCP session was still connected")
	case <-time.After(200 * time.Millisecond):
	}

	tr.mcpSessionDisconnected() // unix=0, mcp=0 → grace
	select {
	case <-tr.done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected grace period to fire after last driver (unix+MCP) dropped to zero")
	}
}

// TestDriverTracker_UnixProbeBeforeFirstMCP_DoesNotShutDown is the regression
// for the post-dev-install failure mode: startDaemonProcess's waitForSocket
// dials the unix socket (unix 0→1→0) before the Cursor proxy has finished
// MCP initialize. Without hadMCPSession gating, that lone disconnect started
// the grace timer and the daemon exited before the driver connected.
func TestDriverTracker_UnixProbeBeforeFirstMCP_DoesNotShutDown(t *testing.T) {
	tr := newDriverTracker(50*time.Millisecond, testTrackerLogger())

	tr.driverConnected()
	tr.driverDisconnected()

	select {
	case <-tr.done():
		t.Fatal("unix probe before any MCP session must not start grace shutdown")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestDriverTracker_MCPSessionReconnect_CancelsGracePeriod ensures that if
// an MCP session connects during the grace window (e.g. an IDE reconnects
// after a brief disconnect), the pending shutdown is cancelled.
func TestDriverTracker_MCPSessionReconnect_CancelsGracePeriod(t *testing.T) {
	tr := newDriverTracker(200*time.Millisecond, testTrackerLogger())

	tr.mcpSessionConnected()
	tr.mcpSessionDisconnected() // grace starts

	// Reconnect inside the grace window.
	time.Sleep(50 * time.Millisecond)
	tr.mcpSessionConnected()

	select {
	case <-tr.done():
		t.Fatal("grace period fired even though an MCP session reconnected within the window")
	case <-time.After(400 * time.Millisecond):
	}
}
