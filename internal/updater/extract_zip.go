package updater

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxZipExtractedBytes int64 = 1 << 30

func extractZip(zipPath, destDir string) error {
	return extractZipWithLimit(zipPath, destDir, maxZipExtractedBytes)
}

func extractZipWithLimit(zipPath, destDir string, maxBytes int64) error {
	if maxBytes <= 0 {
		return fmt.Errorf("invalid zip extraction limit")
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	remaining := maxBytes
	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)
		// Guard against zip slip
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode()); err != nil {
				return fmt.Errorf("create directory %s: %w", f.Name, err)
			}
			continue
		}
		if f.UncompressedSize64 > uint64(remaining) {
			return fmt.Errorf("zip exceeds extracted size limit of %d bytes", maxBytes)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}

		n, err := io.Copy(outFile, io.LimitReader(rc, remaining+1))
		if err != nil || n > remaining {
			outFile.Close()
			rc.Close()
			os.Remove(target)
			if err == nil {
				return fmt.Errorf("zip exceeds extracted size limit of %d bytes", maxBytes)
			}
			return err
		}
		remaining -= n

		// A failed close can mean a truncated write; swallowing it would let
		// the updater install a corrupt binary.
		closeErr := outFile.Close()
		rc.Close()
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", f.Name, closeErr)
		}
	}
	return nil
}
