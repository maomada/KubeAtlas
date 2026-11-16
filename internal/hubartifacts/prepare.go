package hubartifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type PrepareOptions struct {
	ChartDir      string
	LayoutDir     string
	InstallerPath string
	BootstrapPath string
	OutputDir     string
	Version       string
	ChartVersion  string
	Revision      string
	ImageDigest   string
	HelmBinary    string
}

func Prepare(options PrepareOptions) error {
	if !versionPattern.MatchString(options.Version) || !revisionPattern.MatchString(options.Revision) || !digestPattern.MatchString(options.ImageDigest) {
		return errors.New("version, source revision, or image digest is invalid")
	}
	chartVersion := options.ChartVersion
	if chartVersion == "" {
		chartVersion = "0.0.0-dev." + options.Revision[:12]
	}
	if !chartVersionPattern.MatchString(chartVersion) {
		return errors.New("chart version must be valid SemVer")
	}
	if options.HelmBinary == "" {
		options.HelmBinary = "helm"
	}
	chartInfo, err := os.Lstat(options.ChartDir)
	if err != nil || !chartInfo.IsDir() || chartInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("source chart directory is unavailable")
	}
	layoutInfo, err := os.Lstat(options.LayoutDir)
	if err != nil || !layoutInfo.IsDir() || layoutInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("source OCI layout is unavailable")
	}
	installerInfo, err := os.Lstat(options.InstallerPath)
	if err != nil || !installerInfo.Mode().IsRegular() || installerInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("installer source is unavailable")
	}
	if options.BootstrapPath != "" {
		bootstrapInfo, err := os.Lstat(options.BootstrapPath)
		if err != nil || !bootstrapInfo.Mode().IsRegular() || bootstrapInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("bootstrap source is unavailable")
		}
	}
	output, err := filepath.Abs(options.OutputDir)
	if err != nil || output == string(filepath.Separator) {
		return errors.New("output directory is invalid")
	}
	if _, err := os.Lstat(output); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("output directory already exists or cannot be inspected")
	}
	parent := filepath.Dir(output)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 || parentInfo.Mode().Perm() != 0o700 {
		return errors.New("output parent must be an existing private directory with mode 0700")
	}
	stage, err := os.MkdirTemp(parent, ".hub-agent-artifacts-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return err
	}
	packageDir := filepath.Join(stage, ".chart")
	if err := os.Mkdir(packageDir, 0o700); err != nil {
		return err
	}
	cmd := exec.Command(options.HelmBinary, "package", options.ChartDir, "--version", chartVersion, "--app-version", options.Version, "--destination", packageDir)
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("helm package failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	chartFile := "kubeatlas-" + chartVersion + ".tgz"
	chartPath := filepath.Join(packageDir, chartFile)
	if info, err := os.Lstat(chartPath); err != nil || !info.Mode().IsRegular() {
		return errors.New("Helm packaged a chart with a different version or filename")
	}
	if err := os.Rename(chartPath, filepath.Join(stage, chartFile)); err != nil {
		return err
	}
	if err := os.Remove(packageDir); err != nil {
		return err
	}
	if err := copyTree(options.LayoutDir, filepath.Join(stage, "oci")); err != nil {
		return err
	}
	if err := copyRegular(options.InstallerPath, filepath.Join(stage, "install-hub-agent.sh"), 0o444); err != nil {
		return err
	}
	if options.BootstrapPath != "" {
		if err := copyRegular(options.BootstrapPath, filepath.Join(stage, "bootstrap-hub-agent.sh"), 0o444); err != nil {
			return err
		}
	}
	chartHash, err := hashPath(filepath.Join(stage, chartFile))
	if err != nil {
		return err
	}
	installerHash, err := hashPath(filepath.Join(stage, "install-hub-agent.sh"))
	if err != nil {
		return err
	}
	var bootstrap *struct {
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
	}
	if options.BootstrapPath != "" {
		bootstrapHash, err := hashPath(filepath.Join(stage, "bootstrap-hub-agent.sh"))
		if err != nil {
			return err
		}
		bootstrap = &struct {
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
		}{File: "bootstrap-hub-agent.sh", SHA256: bootstrapHash}
	}
	manifest := Manifest{SchemaVersion: 1, Version: options.Version, SourceRevision: options.Revision, Image: struct {
		Digest string `json:"digest"`
	}{Digest: options.ImageDigest}}
	manifest.Chart.File = chartFile
	manifest.Chart.Version = chartVersion
	manifest.Chart.SHA256 = chartHash
	manifest.Installer.File = "install-hub-agent.sh"
	manifest.Installer.SHA256 = installerHash
	manifest.Bootstrap = bootstrap
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), data, 0o444); err != nil {
		return err
	}
	catalog, err := Open(stage, options.Version)
	if err != nil {
		return err
	}
	_ = catalog.Close()
	if err := makeReadOnly(stage); err != nil {
		return err
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output directory appeared during preparation")
	}
	return os.Rename(stage, output)
}

func copyTree(source, destination string) error {
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("source OCI layout escapes its directory")
		}
		target := filepath.Join(destination, relative)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source OCI layout contains an unavailable or symlink entry")
		}
		if entry.IsDir() {
			if relative == "." {
				return nil
			}
			return os.Mkdir(target, 0o700)
		}
		if !info.Mode().IsRegular() {
			return errors.New("source OCI layout contains a non-regular file")
		}
		return copyRegular(path, target, 0o444)
	})
}

func copyRegular(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func hashPath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func makeReadOnly(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o555)
		}
		return os.Chmod(path, 0o444)
	})
}
