package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtract(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"macawlink.exe": "new-exe", "macawlink_native.dll": "new-dll", "../evil.txt": "x"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "macawlink.exe"), []byte("old"), 0o644) // upgrade over an old file
	if _, err := extract(buf.Bytes(), dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "macawlink.exe")); string(b) != "new-exe" {
		t.Fatalf("exe = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "macawlink_native.dll")); string(b) != "new-dll" {
		t.Fatalf("dll = %q", b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); err == nil {
		t.Fatal("zip-slip entry was written outside the install folder")
	}
	os.WriteFile(filepath.Join(dir, "x.dll.old-1"), nil, 0o644)
	cleanupOld(dir)
	if _, err := os.Stat(filepath.Join(dir, "x.dll.old-1")); err == nil {
		t.Fatal("old file not cleaned")
	}
}
