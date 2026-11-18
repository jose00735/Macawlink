package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// extract unpacks the embedded zip. Files still mapped by other processes (the DirectShow
// camera inside a browser, Frame Server) can't be overwritten but can be renamed, so they are
// moved aside as *.old-<time> and cleaned up on the next run. It returns the names of files
// that were in use: the apps holding them keep running the old code until they restart.
func extract(z []byte, dst string) (inUse []string, err error) {
	r, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		return nil, fmt.Errorf("corrupt installer: %w", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	stamp := time.Now().Format("20060102150405")
	for _, f := range r.File {
		if f.FileInfo().IsDir() || strings.Contains(f.Name, "..") {
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(f.Name))
		os.MkdirAll(filepath.Dir(target), 0o755)
		out, err := os.Create(target)
		if err != nil {
			// In use: rename the old one out of the way and try again.
			if rerr := os.Rename(target, target+".old-"+stamp); rerr != nil {
				return inUse, fmt.Errorf("%s is in use; close apps using Macawlink Camera and run setup again (%v)", f.Name, err)
			}
			log.Printf("  %s was in use: moved aside", f.Name)
			inUse = append(inUse, f.Name)
			if out, err = os.Create(target); err != nil {
				return inUse, err
			}
		}
		in, err := f.Open()
		if err != nil {
			out.Close()
			return inUse, err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return inUse, fmt.Errorf("write %s: %w", f.Name, err)
		}
		log.Printf("  %s (%d KB)", f.Name, f.UncompressedSize64/1024)
	}
	return inUse, nil
}

func cleanupOld(dir string) {
	old, _ := filepath.Glob(filepath.Join(dir, "*.old-*"))
	for _, f := range old {
		os.Remove(f) // still locked? stays until the next update
	}
}
