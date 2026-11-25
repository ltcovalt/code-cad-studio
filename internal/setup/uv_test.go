package setup

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUVReleaseAssetsAllDesktopTargets(t *testing.T) {
	targets := []struct {
		goos   string
		goarch string
		isZip  bool
	}{
		{"darwin", "arm64", false},
		{"darwin", "amd64", false},
		{"linux", "amd64", false},
		{"linux", "arm64", false},
		{"windows", "amd64", true},
		{"windows", "arm64", true},
	}

	for _, tc := range targets {
		asset, err := uvAssetForPlatform(tc.goos, tc.goarch)
		if err != nil {
			t.Fatalf("uvAssetForPlatform(%s, %s) failed: %v", tc.goos, tc.goarch, err)
		}
		if asset.ArchiveName == "" {
			t.Fatalf("%s/%s has empty archive name", tc.goos, tc.goarch)
		}
		if asset.IsZip != tc.isZip {
			t.Fatalf("%s/%s IsZip = %v, want %v", tc.goos, tc.goarch, asset.IsZip, tc.isZip)
		}
		if len(asset.SHA256) != 64 {
			t.Fatalf("%s/%s SHA256 length = %d, want 64", tc.goos, tc.goarch, len(asset.SHA256))
		}
		if _, err := hex.DecodeString(asset.SHA256); err != nil {
			t.Fatalf("%s/%s SHA256 invalid hex: %v", tc.goos, tc.goarch, err)
		}
	}

	if _, err := uvAssetForPlatform("plan9", "386"); err == nil {
		t.Fatalf("expected error for unsupported platform")
	}
}

func makeTestTarGz(t *testing.T, entries map[string][]byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for name, content := range entries {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %s: %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("write tar content %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func makeTestZipBytes(t *testing.T, entries map[string][]byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestDownloadUVTarGzAndZip(t *testing.T) {
	t.Run("tar.gz extraction and verification", func(t *testing.T) {
		payload := []byte("#!/bin/sh\necho uv\n")
		archive, sha := makeTestTarGz(t, map[string][]byte{
			"uv-x86_64-unknown-linux-gnu/uv":  payload,
			"uv-x86_64-unknown-linux-gnu/uvx": []byte("ignored"),
		})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/uv-test.tar.gz" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(archive)
		}))
		defer srv.Close()

		target := filepath.Join(t.TempDir(), "tools", "uv")
		var stages []string
		got, err := downloadUV(context.Background(), uvDownloadOptions{
			BaseURL:    srv.URL,
			TargetPath: target,
			GOOS:       "linux",
			GOARCH:     "amd64",
			Asset: &uvReleaseAsset{
				ArchiveName: "uv-test.tar.gz",
				SHA256:      sha,
				IsZip:       false,
			},
		}, func(ev InstallEvent) {
			stages = append(stages, ev.Stage)
		})
		if err != nil {
			t.Fatalf("downloadUV tar.gz failed: %v", err)
		}
		if got != target {
			t.Fatalf("got path %q, want %q", got, target)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("read extracted uv: %v", err)
		}
		if !bytes.Equal(data, payload) {
			t.Fatalf("extracted uv content mismatch")
		}
		if runtime.GOOS != "windows" {
			st, err := os.Stat(target)
			if err != nil {
				t.Fatalf("stat extracted uv: %v", err)
			}
			if st.Mode()&0o111 == 0 {
				t.Fatalf("expected executable bit on extracted uv, mode=%o", st.Mode())
			}
		}
		if len(stages) == 0 || stages[0] != "bootstrapping_uv" {
			t.Fatalf("expected bootstrapping_uv stage events, got %v", stages)
		}
	})

	t.Run("zip extraction and verification", func(t *testing.T) {
		payload := []byte("MZ-fake-uv-exe")
		archive, sha := makeTestZipBytes(t, map[string][]byte{
			"uv.exe":  payload,
			"uvx.exe": []byte("ignored"),
		})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/uv-test.zip" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(archive)
		}))
		defer srv.Close()

		target := filepath.Join(t.TempDir(), "tools", "uv.exe")
		got, err := downloadUV(context.Background(), uvDownloadOptions{
			BaseURL:    srv.URL,
			TargetPath: target,
			GOOS:       "windows",
			GOARCH:     "amd64",
			Asset: &uvReleaseAsset{
				ArchiveName: "uv-test.zip",
				SHA256:      sha,
				IsZip:       true,
			},
		}, nil)
		if err != nil {
			t.Fatalf("downloadUV zip failed: %v", err)
		}
		if got != target {
			t.Fatalf("got path %q, want %q", got, target)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("read extracted uv.exe: %v", err)
		}
		if !bytes.Equal(data, payload) {
			t.Fatalf("extracted uv.exe content mismatch")
		}
	})

	t.Run("rejects checksum mismatch", func(t *testing.T) {
		archive, _ := makeTestTarGz(t, map[string][]byte{
			"uv-x86_64-unknown-linux-gnu/uv": []byte("tampered"),
		})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(archive)
		}))
		defer srv.Close()

		target := filepath.Join(t.TempDir(), "tools", "uv")
		_, err := downloadUV(context.Background(), uvDownloadOptions{
			BaseURL:    srv.URL,
			TargetPath: target,
			GOOS:       "linux",
			GOARCH:     "amd64",
			Asset: &uvReleaseAsset{
				ArchiveName: "uv-test.tar.gz",
				SHA256:      strings.Repeat("0", 64),
				IsZip:       false,
			},
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
			t.Fatalf("expected SHA-256 mismatch error, got %v", err)
		}
		if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
			t.Fatalf("expected target not to exist after checksum mismatch, statErr=%v", statErr)
		}
	})

	t.Run("rejects disallowed host", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "tools", "uv")
		_, err := downloadUV(context.Background(), uvDownloadOptions{
			BaseURL:    "https://evil.example.com/releases",
			TargetPath: target,
			GOOS:       "linux",
			GOARCH:     "amd64",
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "allowlist") {
			t.Fatalf("expected allowlist error, got %v", err)
		}
	})
}
