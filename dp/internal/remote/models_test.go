package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func modelArchive(t *testing.T, entries []tar.Header) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	for i := range entries {
		header := entries[i]
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg && header.Size > 0 {
			if _, err := tw.Write(bytes.Repeat([]byte{'x'}, int(header.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestInspectModelArchiveCommonRoot(t *testing.T) {
	content := modelArchive(t, []tar.Header{
		{Name: "Qwen/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "Qwen/config.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2},
		{Name: "Qwen/model.bin", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
	})
	result, err := inspectModelArchive(bytes.NewReader(content), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !result.StripCommonRoot || result.FileCount != 2 || result.ExpandedSize != 6 || len(result.SHA256) != 64 {
		t.Fatalf("unexpected inspection: %+v", result)
	}
}

func TestInspectModelArchiveAcceptsTarDotRoot(t *testing.T) {
	content := modelArchive(t, []tar.Header{
		{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./config.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2},
		{Name: "./model.bin", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
	})
	result, err := inspectModelArchive(bytes.NewReader(content), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if result.StripCommonRoot || result.FileCount != 2 || result.ExpandedSize != 6 {
		t.Fatalf("unexpected inspection: %+v", result)
	}
}

func TestInspectModelArchiveRejectsDotRootFile(t *testing.T) {
	content := modelArchive(t, []tar.Header{{Name: ".", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}})
	if _, err := inspectModelArchive(bytes.NewReader(content), 1024); err == nil {
		t.Fatal("expected dot root regular file to be rejected")
	}
}

func TestInspectModelArchiveRejectsLinksAndTraversal(t *testing.T) {
	for name, header := range map[string]tar.Header{
		"link":            {Name: "model/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"traversal":       {Name: "../escape", Typeflag: tar.TypeReg, Size: 1},
		"inner traversal": {Name: "model/../escape", Typeflag: tar.TypeReg, Size: 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := inspectModelArchive(bytes.NewReader(modelArchive(t, []tar.Header{header})), 1024)
			if err == nil {
				t.Fatal("expected unsafe archive error")
			}
		})
	}
}

func TestInspectModelArchiveExpandedLimit(t *testing.T) {
	content := modelArchive(t, []tar.Header{{Name: "model.bin", Typeflag: tar.TypeReg, Size: 8}})
	if _, err := inspectModelArchive(bytes.NewReader(content), 4); err == nil {
		t.Fatal("expected expanded size limit")
	}
}
