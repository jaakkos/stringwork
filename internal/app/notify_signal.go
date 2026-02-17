package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// TouchNotifySignal writes a monotonic revision (timestamp) to the signal file
// so fsnotify watchers can detect state changes. Creates parent dir and file if needed.
func TouchNotifySignal(signalPath string) error {
	if signalPath == "" {
		return nil
	}
	dir := filepath.Dir(signalPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create signal file dir: %w", err)
	}
	rev := strconv.FormatInt(time.Now().UnixNano(), 10)
	return os.WriteFile(signalPath, []byte(rev), 0644)
}
