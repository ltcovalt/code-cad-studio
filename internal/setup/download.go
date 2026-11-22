package setup

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DownloadOptions configures downloading the default code-cad starter repository.
type DownloadOptions struct {
	ArchiveURL string
	TargetDir  string
	// AllowOverwrite permits extracting into a non-empty existing directory by copying
	// models/ and utils/ and overwriting any matching files in those folders.
	AllowOverwrite bool
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

// DownloadStarterProject fetches a GitHub zip archive and safely extracts it into TargetDir.
func DownloadStarterProject(
	ctx context.Context,
	opts DownloadOptions,
	onProgress func(DownloadProgress),
) (DownloadResult, error) {
	url := strings.TrimSpace(opts.ArchiveURL)
	if url == "" {
		url = DefaultArchiveURL
	}
	dest := ExpandPath(opts.TargetDir)
	if dest == "" {
		return DownloadResult{}, errors.New("target project directory is required")
	}

	absDest, err := filepath.Abs(dest)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("resolve target directory: %w", err)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "Code-CAD-Studio")

	client := &http.Client{
		Timeout: 5 * time.Minute,
	}
	resp, err := client.Do(req)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("download archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DownloadResult{}, fmt.Errorf("download failed with HTTP %d (%s)", resp.StatusCode, resp.Status)
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

	for {
		if err := ctx.Err(); err != nil {
			return DownloadResult{}, err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmpFile.Write(buf[:n]); werr != nil {
				return DownloadResult{}, fmt.Errorf("write temp archive: %w", werr)
			}
			downloaded += int64(n)
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

	stripPrefix := detectSingleRootPrefix(zr.File)

	if err := os.MkdirAll(absDest, 0o755); err != nil {
		return 0, 0, fmt.Errorf("create target directory: %w", err)
	}

	totalFiles := len(zr.File)
	processed := 0
	lastEmit := time.Now()

	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return filesAdded, filesOverwritten, err
		}

		// Reject symlinks for safety
		if f.Mode()&os.ModeSymlink != 0 {
			continue
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

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, 0o755); err != nil {
				return filesAdded, filesOverwritten, fmt.Errorf("create directory %s: %w", relName, err)
			}
			processed++
			continue
		}

		existed := false
		if mergeMode {
			if _, statErr := os.Stat(outPath); statErr == nil {
				existed = true
			}
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return filesAdded, filesOverwritten, fmt.Errorf("create parent directory for %s: %w", relName, err)
		}

		if err := copyZipEntryToFile(f, outPath); err != nil {
			return filesAdded, filesOverwritten, fmt.Errorf("extract %s: %w", relName, err)
		}
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

func copyZipEntryToFile(f *zip.File, outPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := os.FileMode(0o644)
	if f.Mode()&0o111 != 0 {
		mode = 0o755
	}

	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}
