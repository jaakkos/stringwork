package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTouchNotifySignal_EmptyPath(t *testing.T) {
	err := TouchNotifySignal("")
	if err != nil {
		t.Errorf("TouchNotifySignal(\"\") should not error, got %v", err)
	}
}

func TestTouchNotifySignal_CreatesFileAndDir(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, "subdir", ".stringwork-notify")
	err := TouchNotifySignal(signalPath)
	if err != nil {
		t.Fatalf("TouchNotifySignal: %v", err)
	}
	info, err := os.Stat(signalPath)
	if err != nil {
		t.Fatalf("signal file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("signal file should contain revision (non-empty)")
	}
}

func TestTouchNotifySignal_OverwritesWithNewRevision(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, ".notify")
	if err := TouchNotifySignal(signalPath); err != nil {
		t.Fatal(err)
	}
	data1, _ := os.ReadFile(signalPath)
	if err := TouchNotifySignal(signalPath); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(signalPath)
	if string(data1) == string(data2) {
		t.Error("second touch should write new revision (timestamp)")
	}
}
