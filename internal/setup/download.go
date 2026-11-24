package setup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxArchiveBytes        int64 = 100 * 1024 * 1024 // 100 MiB
	maxZipEntries          int   = 5000
	maxSingleFileBytes     int64 = 50 * 1024 * 1024  // 50 MiB
	maxTotalExtractedBytes int64 = 250 * 1024 * 1024 // 250 MiB
)

var defaultAllowedArchiveHosts = []string{
	"github.com",
	"codeload.github.com",
	"objects.githubusercontent.com",
}

// DownloadOptions configures downloading the default code-cad starter repository.
type DownloadOptions struct {
	ArchiveURL string
	TargetDir  string
	// AllowOverwrite permits extracting into a non-empty existing directory by copying
	// models/ and utils/ and overwriting any matching files in those folders.
	AllowOverwrite bool
	ExpectedSHA256 string
	AllowedHosts   []string
}

// DownloadProgress reports progress while downloading and extracting the starter repo.
type DownloadProgress struct {
	Stage           string `json:"stage"` // downloading, extracting, done
	Message         string `json:"message"`
	BytesDownloaded int64  `json:"bytes_downloaded,omitempty"`
	TotalBytes      int64  `json:"total_bytes,omitempty"`
	FilesExtracted  int    `json:"files_extracted,omitempty"`
	TotalFiles      int    `json:"total_files,omitempty"`
}

// DownloadResult summarizes the downloaded/merged starter project.
type DownloadResult struct {
	ProjectInfo      ProjectDirInfo `json:"project_info"`
	FilesAdded       int            `json:"files_added"`
	FilesOverwritten int            `json:"files_overwritten"`
	Merged           bool           `json:"merged"`
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func validateDownloadURL(rawURL string, allowedHosts []string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid download URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return errors.New("download URL missing host")
	}

	if isLoopbackHost(host) {
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("unsupported URL scheme %q for loopback host", parsed.Scheme)
		}
		return nil
	}

	if scheme != "https" {
		return fmt.Errorf("refusing non-HTTPS archive URL %q", rawURL)
	}

	allowlist := allowedHosts
	if len(allowlist) == 0 {
		allowlist = defaultAllowedArchiveHosts
	}
	for _, allowed := range allowlist {
		if strings.EqualFold(strings.TrimSpace(allowed), host) {
			return nil
		}
	}
	return fmt.Errorf("archive host %q is not in allowlist", host)
}

// DownloadStarterProject fetches a GitHub zip archive and safely extracts it into TargetDir.
func DownloadStarterProject(
	ctx context.Context,
	opts DownloadOptions,
	onProgress func(DownloadProgress),
) (DownloadResult, error) {
	rawURL := strings.TrimSpace(opts.ArchiveURL)
	if rawURL == "" {
		rawURL = DefaultArchiveURL
	}
	if err := validateDownloadURL(rawURL, opts.AllowedHosts); err != nil {
		return DownloadResult{}, err
	}

	expectedSHA := strings.ToLower(strings.TrimSpace(opts.ExpectedSHA256))
	if expectedSHA != "" {
		if len(expectedSHA) != 64 {
			return DownloadResult{}, fmt.Errorf("invalid expected_sha256 length (%d)", len(expectedSHA))
		}
		if _, err := hex.DecodeString(expectedSHA); err != nil {
			return DownloadResult{}, fmt.Errorf("invalid expected_sha256 hex: %w", err)
		}
	}

	dest := ExpandPath(opts.TargetDir)
	if dest == "" {
		return DownloadResult{}, errors.New("target project directory is required")
	}

	absDest, err := filepath.Abs(dest)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("resolve target directory: %w", err)
	}

	if info, lerr := os.Lstat(absDest); lerr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return DownloadResult{}, fmt.Errorf("refusing to extract into symlinked target directory: %s", absDest)
		}
	}

	mergeMode := false
	if st, err := os.Stat(absDest); err == nil {
		if !st.IsDir() {
			return DownloadResult{}, fmt.Errorf("target path exists and is not a directory: %s", absDest)
		}
		entries, rerr := os.ReadDir(absDest)
		if rerr != nil {
			return DownloadResult{}, fmt.Errorf("read target directory: %w", rerr)
		}
		if len(entries) > 0 {
			if !opts.AllowOverwrite {
				return DownloadResult{}, fmt.Errorf("target directory is not empty: %s", absDest)
			}
			mergeMode = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return DownloadResult{}, fmt.Errorf("check target directory: %w", err)
	}

	emit := func(p DownloadProgress) {
		if onProgress != nil {
			onProgress(p)
		}
	}

	emit(DownloadProgress{
		Stage:   "downloading",
		Message: "Connecting to GitHub...",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "Code-CAD-Studio")

	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return validateDownloadURL(r.URL.String(), opts.AllowedHosts)
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("download archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DownloadResult{}, fmt.Errorf("download failed with HTTP %d (%s)", resp.StatusCode, resp.Status)
	}
	if resp.ContentLength > maxArchiveBytes {
		return DownloadResult{}, fmt.Errorf("archive exceeds maximum allowed size (%d bytes)", maxArchiveBytes)
	}

	tmpFile, err := os.CreateTemp("", "code-cad-starter-*.zip")
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create temp archive file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	totalBytes := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 32*1024)
	lastEmit := time.Now()
	hasher := sha256.New()
	limitedBody := io.LimitReader(resp.Body, maxArchiveBytes+1)

	for {
		if err := ctx.Err(); err != nil {
			return DownloadResult{}, err
		}
		n, rerr := limitedBody.Read(buf)
		if n > 0 {
			downloaded += int64(n)
			if downloaded > maxArchiveBytes {
				return DownloadResult{}, fmt.Errorf("archive exceeds maximum allowed size (%d bytes)", maxArchiveBytes)
			}
			if _, werr := tmpFile.Write(buf[:n]); werr != nil {
				return DownloadResult{}, fmt.Errorf("write temp archive: %w", werr)
			}
			_, _ = hasher.Write(buf[:n])
			if time.Since(lastEmit) >= 100*time.Millisecond {
				emit(DownloadProgress{
					Stage:           "downloading",
					Message:         "Downloading code-cad starter library...",
					BytesDownloaded: downloaded,
					TotalBytes:      totalBytes,
				})
				lastEmit = time.Now()
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return DownloadResult{}, fmt.Errorf("read download stream: %w", rerr)
		}
	}

	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA != "" && subtle.ConstantTimeCompare([]byte(actualSHA), []byte(expectedSHA)) != 1 {
		return DownloadResult{}, fmt.Errorf("archive SHA-256 mismatch: got %s, want %s", actualSHA, expectedSHA)
	}

	emit(DownloadProgress{
		Stage:           "downloading",
		Message:         "Download complete. Extracting files...",
		BytesDownloaded: downloaded,
		TotalBytes:      totalBytes,
	})

	if err := tmpFile.Close(); err != nil {
		return DownloadResult{}, fmt.Errorf("close temp archive: %w", err)
	}

	added, overwritten, err := extractZipSafe(ctx, tmpPath, absDest, mergeMode, emit)
	if err != nil {
		return DownloadResult{}, err
	}

	info := InspectProjectDir(absDest)
	doneMsg := fmt.Sprintf("Downloaded starter library (%d models found).", info.ModelCount)
	if mergeMode {
		doneMsg = fmt.Sprintf("Downloaded starter models (%d added, %d overwritten).", added, overwritten)
	}
	emit(DownloadProgress{
		Stage:   "done",
		Message: doneMsg,
	})
	return DownloadResult{
		ProjectInfo:      info,
		FilesAdded:       added,
		FilesOverwritten: overwritten,
		Merged:           mergeMode,
	}, nil
}

func isMergeAllowedPath(relName string) bool {
	return relName == "models" ||
		strings.HasPrefix(relName, "models/") ||
		relName == "utils" ||
		strings.HasPrefix(relName, "utils/")
}

// ensureNoSymlink verifies via os.Lstat that neither absDest nor any intermediate
// or final path component up to outPath is an existing symlink.
func ensureNoSymlink(absDest, outPath string) error {
	if info, err := os.Lstat(absDest); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink: %s", absDest)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	rel, err := filepath.Rel(absDest, outPath)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	cur := absDest
	for _, seg := range strings.Split(rel, string(os.PathSeparator)) {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink: %s", cur)
		}
	}
	return nil
}

func extractZipSafe(
	ctx context.Context,
	zipPath string,
	absDest string,
	mergeMode bool,
	emit func(DownloadProgress),
) (filesAdded int, filesOverwritten int, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, 0, fmt.Errorf("open zip archive: %w", err)
	}
	defer zr.Close()

	if len(zr.File) > maxZipEntries {
		return 0, 0, fmt.Errorf("archive contains too many entries (%d > %d)", len(zr.File), maxZipEntries)
	}

	stripPrefix := detectSingleRootPrefix(zr.File)

	if err := ensureNoSymlink(absDest, absDest); err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(absDest, 0o755); err != nil {
		return 0, 0, fmt.Errorf("create target directory: %w", err)
	}

	totalFiles := len(zr.File)
	processed := 0
	var totalExtracted int64
	lastEmit := time.Now()

	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return filesAdded, filesOverwritten, err
		}

		// Reject symlink entries inside the zip
		if f.Mode()&os.ModeSymlink != 0 {
			return filesAdded, filesOverwritten, fmt.Errorf("archive contains unsupported symlink entry: %q", f.Name)
		}

		// Normalize zip entry name (zip spec uses forward slashes)
		rawName := strings.ReplaceAll(f.Name, `\`, "/")
		if err := validateZipEntryPath(rawName); err != nil {
			return filesAdded, filesOverwritten, err
		}

		relName := rawName
		if stripPrefix != "" {
			if !strings.HasPrefix(relName, stripPrefix) {
				continue
			}
			relName = strings.TrimPrefix(relName, stripPrefix)
		}
		relName = strings.TrimPrefix(relName, "/")
		if relName == "" {
			continue
		}
		if err := validateZipEntryPath(relName); err != nil {
			return filesAdded, filesOverwritten, err
		}

		// In merge mode, only copy models/ and utils/ so we never clutter an existing repo.
		if mergeMode && !isMergeAllowedPath(relName) {
			processed++
			continue
		}

		outPath := filepath.Join(absDest, filepath.FromSlash(relName))
		relCheck, err := filepath.Rel(absDest, outPath)
		if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(os.PathSeparator)) || filepath.IsAbs(relCheck) {
			return filesAdded, filesOverwritten, fmt.Errorf("unsafe archive path detected: %q", f.Name)
		}

		// Unconditionally verify no component in absDest..outPath is an on-disk symlink.
		if err := ensureNoSymlink(absDest, outPath); err != nil {
			return filesAdded, filesOverwritten, err
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, 0o755); err != nil {
				return filesAdded, filesOverwritten, fmt.Errorf("create directory %s: %w", relName, err)
			}
			processed++
			continue
		}

		if f.UncompressedSize64 > uint64(maxSingleFileBytes) {
			return filesAdded, filesOverwritten, fmt.Errorf("archive entry %q exceeds single-file limit (%d bytes)", relName, maxSingleFileBytes)
		}
		if totalExtracted+int64(f.UncompressedSize64) > maxTotalExtractedBytes {
			return filesAdded, filesOverwritten, fmt.Errorf("archive total uncompressed size exceeds limit (%d bytes)", maxTotalExtractedBytes)
		}

		existed := false
		if info, lstatErr := os.Lstat(outPath); lstatErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return filesAdded, filesOverwritten, fmt.Errorf("refusing to write through symlink: %s", outPath)
			}
			existed = true
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return filesAdded, filesOverwritten, fmt.Errorf("create parent directory for %s: %w", relName, err)
		}

		written, err := copyZipEntryToFile(f, outPath, maxTotalExtractedBytes-totalExtracted)
		if err != nil {
			return filesAdded, filesOverwritten, fmt.Errorf("extract %s: %w", relName, err)
		}
		totalExtracted += written
		if existed {
			filesOverwritten++
		} else {
			filesAdded++
		}
		processed++

		if time.Since(lastEmit) >= 100*time.Millisecond {
			emit(DownloadProgress{
				Stage:          "extracting",
				Message:        fmt.Sprintf("Extracting files (%d / %d)...", processed, totalFiles),
				FilesExtracted: processed,
				TotalFiles:     totalFiles,
			})
			lastEmit = time.Now()
		}
	}

	emit(DownloadProgress{
		Stage:          "extracting",
		Message:        fmt.Sprintf("Extracted %d files.", filesAdded+filesOverwritten),
		FilesExtracted: processed,
		TotalFiles:     totalFiles,
	})
	return filesAdded, filesOverwritten, nil
}

func validateZipEntryPath(p string) error {
	if strings.HasPrefix(p, "/") || (len(p) >= 2 && p[1] == ':') {
		return fmt.Errorf("unsafe absolute archive path: %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("unsafe parent traversal in archive path: %q", p)
		}
	}
	return nil
}

func detectSingleRootPrefix(files []*zip.File) string {
	var root string
	for _, f := range files {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		name = strings.TrimPrefix(name, "/")
		if name == "" {
			continue
		}
		idx := strings.Index(name, "/")
		if idx <= 0 {
			// File directly at archive root -> do not strip a prefix
			return ""
		}
		seg := name[:idx+1]
		if root == "" {
			root = seg
		} else if root != seg {
			return ""
		}
	}
	return root
}

func copyZipEntryToFile(f *zip.File, outPath string, remainingTotal int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	mode := os.FileMode(0o644)
	if f.Mode()&0o111 != 0 {
		mode = 0o755
	}

	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}

	limit := maxSingleFileBytes
	if remainingTotal < limit {
		limit = remainingTotal
	}
	lr := io.LimitReader(rc, limit+1)
	written, copyErr := io.Copy(out, lr)
	closeErr := out.Close()

	if copyErr != nil {
		_ = os.Remove(outPath)
		return 0, copyErr
	}
	if written > maxSingleFileBytes {
		_ = os.Remove(outPath)
		return 0, fmt.Errorf("entry exceeds maximum single-file size (%d bytes)", maxSingleFileBytes)
	}
	if written > remainingTotal {
		_ = os.Remove(outPath)
		return 0, fmt.Errorf("archive total uncompressed size exceeds limit (%d bytes)", maxTotalExtractedBytes)
	}
	if closeErr != nil {
		return 0, closeErr
	}
	return written, nil
}
