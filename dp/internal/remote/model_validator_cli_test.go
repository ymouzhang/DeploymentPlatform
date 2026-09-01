package remote

import (
	"archive/tar"
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunModelValidatorCLI(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "model.tar.gz")
	content := modelArchive(t, []tar.Header{
		{Name: "model/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "model/config.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2},
		{Name: "model/model.bin", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
	})
	if err := os.WriteFile(archivePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := RunModelValidatorCLI([]string{"--archive", archivePath, "--max-expanded", "1024"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	events := decodeValidatorEvents(t, stdout.Bytes())
	if events[0].Type != "hello" || events[0].Protocol != modelValidatorProtocol {
		t.Fatalf("unexpected hello: %+v", events[0])
	}
	result := events[len(events)-1]
	if result.Type != "result" || result.Inspection == nil || result.Inspection.FileCount != 2 || result.Inspection.ExpandedSize != 6 || !result.Inspection.StripCommonRoot {
		t.Fatalf("unexpected result: %+v", result)
	}
	foundProgress := false
	for _, event := range events {
		if event.Type == "progress" && event.TotalBytes == int64(len(content)) {
			foundProgress = true
		}
	}
	if !foundProgress {
		t.Fatal("expected progress event")
	}
}

func TestRunModelValidatorCLIReportsUnsafeArchive(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "unsafe.tar.gz")
	content := modelArchive(t, []tar.Header{{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}})
	if err := os.WriteFile(archivePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := RunModelValidatorCLI([]string{"--archive", archivePath, "--max-expanded", "1024"}, &stdout, &stderr); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	events := decodeValidatorEvents(t, stdout.Bytes())
	result := events[len(events)-1]
	if result.Type != "error" || result.ErrorCode != "MODEL_ARCHIVE_UNSAFE" {
		t.Fatalf("unexpected error: %+v", result)
	}
}

func decodeValidatorEvents(t *testing.T, content []byte) []modelValidatorEvent {
	t.Helper()
	var events []modelValidatorEvent
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		var event modelValidatorEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected validator events")
	}
	return events
}
