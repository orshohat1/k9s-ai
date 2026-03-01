// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ai

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// copilotVersion is the pinned version of the Copilot CLI.
	copilotVersion = "0.0.420"

	// npmRegistryURL is the base URL for the npm registry.
	npmRegistryURL = "https://registry.npmjs.org"

	// cacheDirName is the subdirectory under the user's cache dir.
	cacheDirName = "k9s-ai"
)

// platformPackage maps GOOS/GOARCH to the npm package name suffix.
var platformPackage = map[string]string{
	"darwin/arm64":  "darwin-arm64",
	"darwin/amd64":  "darwin-x64",
	"linux/amd64":   "linux-x64",
	"linux/arm64":   "linux-arm64",
	"windows/amd64": "win32-x64",
	"windows/arm64": "win32-arm64",
}

// ResolveCopilotCLIPath finds or installs the copilot CLI binary.
// Resolution order:
//  1. COPILOT_CLI_PATH environment variable
//  2. "copilot" in $PATH
//  3. Cached binary in user cache dir (previously downloaded)
//  4. Auto-download from npm registry
func ResolveCopilotCLIPath(log *slog.Logger) string {
	// 1. Env override.
	if p := os.Getenv("COPILOT_CLI_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// 2. Already in PATH.
	if p, err := exec.LookPath("copilot"); err == nil {
		return p
	}

	// 3. Check cache.
	cacheDir, err := copilotCacheDir()
	if err != nil {
		log.Warn("Cannot determine cache dir for copilot CLI", "error", err)
		return ""
	}
	cachedPath := filepath.Join(cacheDir, copilotBinaryName())
	if _, err := os.Stat(cachedPath); err == nil {
		log.Info("Using cached copilot CLI", "path", cachedPath)
		return cachedPath
	}

	// 4. Auto-download.
	log.Info("Copilot CLI not found, downloading...")
	path, err := downloadCopilotCLI(cacheDir, log)
	if err != nil {
		log.Error("Failed to download copilot CLI", "error", err)
		log.Info("Install manually: npm install -g @github/copilot")
		return ""
	}

	return path
}

func copilotCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, cacheDirName, "cli", copilotVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}
	return dir, nil
}

func copilotBinaryName() string {
	if runtime.GOOS == "windows" {
		return "copilot.exe"
	}
	return "copilot"
}

// downloadCopilotCLI downloads the platform-specific copilot CLI from npm.
func downloadCopilotCLI(cacheDir string, log *slog.Logger) (string, error) {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	pkg, ok := platformPackage[platform]
	if !ok {
		return "", fmt.Errorf("unsupported platform: %s", platform)
	}

	tarURL, err := resolveTarballURL(pkg)
	if err != nil {
		return "", fmt.Errorf("resolving download URL: %w", err)
	}
	log.Info("Downloading copilot CLI", "url", tarURL)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(tarURL)
	if err != nil {
		return "", fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	binaryPath := filepath.Join(cacheDir, copilotBinaryName())
	if err := extractCopilotBinary(resp.Body, binaryPath); err != nil {
		return "", fmt.Errorf("extracting: %w", err)
	}

	log.Info("Copilot CLI installed", "path", binaryPath)
	return binaryPath, nil
}

// resolveTarballURL fetches the tarball URL for a specific version from npm.
func resolveTarballURL(platformSuffix string) (string, error) {
	scope := "@github"
	name := "copilot-" + platformSuffix
	url := fmt.Sprintf("%s/%s/%s/%s", npmRegistryURL, scope, name, copilotVersion)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm registry returned %d for %s/%s@%s", resp.StatusCode, scope, name, copilotVersion)
	}

	var meta struct {
		Dist struct {
			Tarball string `json:"tarball"`
		} `json:"dist"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", fmt.Errorf("parsing npm metadata: %w", err)
	}
	if meta.Dist.Tarball == "" {
		return "", fmt.Errorf("no tarball URL in npm metadata")
	}

	return meta.Dist.Tarball, nil
}

// extractCopilotBinary extracts the copilot binary from an npm tarball (.tgz).
// npm tarballs contain files under "package/", e.g., "package/copilot".
func extractCopilotBinary(r io.Reader, destPath string) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	binaryName := copilotBinaryName()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("copilot binary not found in archive")
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}

		// npm tarballs have files under "package/" prefix.
		name := filepath.Base(hdr.Name)
		if !strings.HasPrefix(name, "copilot") {
			continue
		}
		// Match the binary — skip LICENSE, README, package.json, etc.
		if name != binaryName && name != "copilot-"+platformSuffix() && name != "copilot" {
			continue
		}

		f, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return fmt.Errorf("creating file: %w", err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("writing binary: %w", err)
		}
		f.Close()
		return nil
	}
}

func platformSuffix() string {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if s, ok := platformPackage[platform]; ok {
		return s
	}
	return ""
}
