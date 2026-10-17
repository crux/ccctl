// Package archive implements zip create / extract / validate for
// session archival. Used by `claudectl archive` and `claudectl restore`.
package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Create writes a zip at outPath containing each relative path in paths
// (resolved against srcBase). Directories are walked recursively; files
// are stored with their path RELATIVE to srcBase as the entry name.
func Create(outPath, srcBase string, paths []string) error {
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	for _, rel := range paths {
		if err := addEntry(zw, srcBase, rel); err != nil {
			zw.Close()
			return err
		}
	}
	return zw.Close()
}

func addEntry(zw *zip.Writer, srcBase, rel string) error {
	full := filepath.Join(srcBase, rel)
	info, err := os.Stat(full)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return addFile(zw, srcBase, rel)
	}
	return filepath.WalkDir(full, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		childRel, err := filepath.Rel(srcBase, path)
		if err != nil {
			return err
		}
		return addFile(zw, srcBase, childRel)
	})
}

func addFile(zw *zip.Writer, srcBase, rel string) error {
	full := filepath.Join(srcBase, rel)
	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(rel)
	header.Method = zip.Deflate
	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// Validate opens a zip and reads every entry's CRC to confirm integrity.
// Returns nil on success, error on any corruption.
func Validate(zipPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	return nil
}

// Extract extracts zipPath into destDir, preserving file modes. Guards
// against path traversal (zip-slip).
func Extract(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	destAbs, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}

	for _, f := range r.File {
		target := filepath.Join(destAbs, f.Name)
		clean := filepath.Clean(target)
		if clean != destAbs && !strings.HasPrefix(clean, destAbs+string(os.PathSeparator)) {
			return fmt.Errorf("illegal path in archive: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(clean, f.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
