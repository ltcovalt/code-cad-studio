package setup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ltcovalt/code-cad-studio/internal/config"
)

const (
	// ManagedPythonVersion is the Python minor version provisioned by uv for Studio's shared venv.
	ManagedPythonVersion = "3.14"

	// PinnedUVVersion is the verified astral-sh/uv release downloaded when uv is not already installed.
	PinnedUVVersion = "0.13.10"

	uvReleaseBaseURL  = "https://github.com/astral-sh/uv/releases/download/" + PinnedUVVersion
	maxUVArchiveBytes = 100 * 1024 * 1024 // 100 MiB
	maxUVBinaryBytes  = 128 * 1024 * 1024 // 128 MiB
)

var uvAllowedArchiveHosts = []string{
	"github.com",
	"release-assets.githubusercontent.com",
	"objects.githubusercontent.com",
}

type uvReleaseAsset struct {
	ArchiveName string
	SHA256      string
	IsZip       bool
}

// uvReleaseAssets maps "<goos>/<goarch>" to the official astral-sh/uv release archive
// and its verified SHA-256 digest from sha256.sum.
var uvReleaseAssets = map[string]uvReleaseAsset{
	"darwin/arm64": {
		ArchiveName: "uv-aarch64-apple-darwin.tar.gz",
		SHA256:      "e419bf07533bdbf48795fc793601c834dd79db57a8c49ce8e25cfe3a8639958d",
	},
	"darwin/amd64": {
		ArchiveName: "uv-x86_64-apple-darwin.tar.gz",
		SHA256:      "153e03197588204c04fc4c81cc4bafbfa4280222a385e4ee36a87aeec0ae63b4",
	},
	"linux/amd64": {
		ArchiveName: "uv-x86_64-unknown-linux-gnu.tar.gz",
		SHA256:      "bf65ac1a0af10f92c522d91ac096c7c0a39657647b155577e7836c2cbbb4526c",
	},
	"linux/arm64": {
		ArchiveName: "uv-aarch64-unknown-linux-gnu.tar.gz",
		SHA256:      "14590f644805a6234ffe071154340669e38137f38e60aa9629690119704107d2",
	},
	"windows/amd64": {
		ArchiveName: "uv-x86_64-pc-windows-msvc.zip",
		SHA256:      "297b3881fe1adfb922becc2c1613202abb41dc83ea5ff43d472aa087ca1f8683",
		IsZip:       true,
	},
	"windows/arm64": {
		ArchiveName: "uv-aarch64-pc-windows-msvc.zip",
		SHA256:      "f223bcd3fa010174ffeeb885cc1d5334bb81388640c65baa5b8b42b47017337e",
		IsZip:       true,
	},
}

type uvDownloadOptions struct {
	BaseURL      string
	TargetPath   string
	GOOS         string
	GOARCH       string
	AllowedHosts []string
	Asset        *uvReleaseAsset
}

// EnsureUV returns the path to an existing uv executable if found on PATH, in standard
// user locations, or in Studio's managed tools directory. If uv is not installed, it
// downloads the pinned astral-sh/uv release, verifies its SHA-256 digest, and installs
// it atomically into Studio's managed tools directory.
func EnsureUV(ctx context.Context, onEvent func(InstallEvent)) (string, bool, error) {
	if existing, ok := FindUV(); ok {
		return existing, false, nil
	}
	uvPath, err := downloadUV(ctx, uvDownloadOptions{}, onEvent)
	if err != nil {
		return "", false, err
	}
	return uvPath, true, nil
}

func uvAssetForPlatform(goos, goarch string) (uvReleaseAsset, error) {
	key := goos + "/" + goarch
	asset, ok := uvReleaseAssets[key]
	if !ok {
		return uvReleaseAsset{}, fmt.Errorf("automatic uv download is not supported on %s/%s; install uv manually and retry", goos, goarch)
	}
	return asset, nil
}

func downloadUV(ctx context.Context, opts uvDownloadOptions, onEvent func(InstallEvent)) (string, error) {
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	var asset uvReleaseAsset
	if opts.Asset != nil {
		asset = *opts.Asset
	} else {
		var err error
		asset, err = uvAssetForPlatform(goos, goarch)
		if err != nil {
			return "", err
		}
	}

	expectedSHA := strings.ToLower(strings.TrimSpace(asset.SHA256))
	if len(expectedSHA) != 64 {
		return "", fmt.Errorf("invalid uv archive SHA-256 configuration (%d chars)", len(expectedSHA))
	}
	if _, err := hex.DecodeString(expectedSHA); err != nil {
		return "", fmt.Errorf("invalid uv archive SHA-256 hex: %w", err)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = uvReleaseBaseURL
	}
	archiveURL := baseURL + "/" + asset.ArchiveName

	allowedHosts := opts.AllowedHosts
	if len(allowedHosts) == 0 {
		allowedHosts = uvAllowedArchiveHosts
	}
	if err := validateDownloadURL(archiveURL, allowedHosts); err != nil {
		return "", err
	}

	targetPath := ExpandPath(opts.TargetPath)
	if targetPath == "" {
		var err error
		targetPath, err = config.StudioUVPath()
		if err != nil {
			return "", fmt.Errorf("resolve studio uv path: %w", err)
		}
	}
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("create studio tools directory: %w", err)
	}

	emit := func(ev InstallEvent) {
		if onEvent != nil {
			onEvent(ev)
		}
	}

	emit(InstallEvent{
		Stage:   "bootstrapping_uv",
		Message: fmt.Sprintf("Downloading uv %s...", PinnedUVVersion),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return "", fmt.Errorf("create uv download request: %w", err)
	}
	req.Header.Set("User-Agent", "Code-CAD-Studio")

	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return validateDownloadURL(r.URL.String(), allowedHosts)
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download uv archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download uv archive failed with HTTP %d (%s)", resp.StatusCode, resp.Status)
	}
	if resp.ContentLength > maxUVArchiveBytes {
		return "", fmt.Errorf("uv archive exceeds maximum allowed size (%d bytes)", maxUVArchiveBytes)
	}

	tmpArchive, err := os.CreateTemp("", "code-cad-uv-*")
	if err != nil {
		return "", fmt.Errorf("create temp uv archive: %w", err)
	}
	tmpArchivePath := tmpArchive.Name()
	defer func() {
		_ = tmpArchive.Close()
		_ = os.Remove(tmpArchivePath)
	}()

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxUVArchiveBytes+1)
	buf := make([]byte, 32*1024)
	var downloaded int64

	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := limited.Read(buf)
		if n > 0 {
			downloaded += int64(n)
			if downloaded > maxUVArchiveBytes {
				return "", fmt.Errorf("uv archive exceeds maximum allowed size (%d bytes)", maxUVArchiveBytes)
			}
			if _, werr := tmpArchive.Write(buf[:n]); werr != nil {
				return "", fmt.Errorf("write temp uv archive: %w", werr)
			}
			_, _ = hasher.Write(buf[:n])
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return "", fmt.Errorf("read uv download stream: %w", rerr)
		}
	}

	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(actualSHA), []byte(expectedSHA)) != 1 {
		return "", fmt.Errorf("uv archive SHA-256 mismatch: got %s, want %s", actualSHA, expectedSHA)
	}

	if err := tmpArchive.Close(); err != nil {
		return "", fmt.Errorf("close temp uv archive: %w", err)
	}

	emit(InstallEvent{
		Stage:   "bootstrapping_uv",
		Message: "Verifying and extracting uv...",
	})

	binaryName := "uv"
	if goos == "windows" {
		binaryName = "uv.exe"
	}

	tmpBin, err := os.CreateTemp(targetDir, ".uv-tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp uv binary: %w", err)
	}
	tmpBinPath := tmpBin.Name()
	cleanupTmpBin := true
	defer func() {
		if cleanupTmpBin {
			_ = tmpBin.Close()
			_ = os.Remove(tmpBinPath)
		}
	}()

	if asset.IsZip {
		if err := extractUVFromZip(tmpArchivePath, binaryName, tmpBin); err != nil {
			return "", err
		}
	} else {
		if err := extractUVFromTarGz(tmpArchivePath, binaryName, tmpBin); err != nil {
			return "", err
		}
	}

	if err := tmpBin.Chmod(0o755); err != nil {
		return "", fmt.Errorf("chmod uv binary: %w", err)
	}
	if err := tmpBin.Close(); err != nil {
		return "", fmt.Errorf("close uv binary: %w", err)
	}
	if err := os.Rename(tmpBinPath, targetPath); err != nil {
		return "", fmt.Errorf("install uv binary: %w", err)
	}
	cleanupTmpBin = false

	emit(InstallEvent{
		Stage:   "bootstrapping_uv",
		Message: fmt.Sprintf("uv %s ready.", PinnedUVVersion),
	})
	return targetPath, nil
}

func extractUVFromTarGz(archivePath, binaryName string, dst io.Writer) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open uv tar.gz archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("decompress uv tar.gz archive: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read uv tar archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if path.Base(strings.ReplaceAll(hdr.Name, `\`, "/")) != binaryName {
			continue
		}
		return copyLimitedBinary(tr, dst, binaryName)
	}
	return fmt.Errorf("uv binary %q not found in tar.gz archive", binaryName)
}

func extractUVFromZip(archivePath, binaryName string, dst io.Writer) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open uv zip archive: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !strings.EqualFold(path.Base(strings.ReplaceAll(f.Name, `\`, "/")), binaryName) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open %s in uv zip archive: %w", binaryName, err)
		}
		copyErr := copyLimitedBinary(rc, dst, binaryName)
		_ = rc.Close()
		return copyErr
	}
	return fmt.Errorf("uv binary %q not found in zip archive", binaryName)
}

func copyLimitedBinary(src io.Reader, dst io.Writer, binaryName string) error {
	n, err := io.Copy(dst, io.LimitReader(src, maxUVBinaryBytes+1))
	if err != nil {
		return fmt.Errorf("extract %s: %w", binaryName, err)
	}
	if n > maxUVBinaryBytes {
		return fmt.Errorf("extracted %s exceeds maximum allowed size (%d bytes)", binaryName, maxUVBinaryBytes)
	}
	if n == 0 {
		return fmt.Errorf("extracted %s is empty", binaryName)
	}
	return nil
}
