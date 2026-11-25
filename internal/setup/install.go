package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ltcovalt/code-cad-studio/internal/config"
)

// InstallOptions configures automated shared Studio virtual environment creation and package setup.
type InstallOptions struct {
	LibraryRoots  []string
	TargetVenvDir string // defaults to config.StudioVenvDir()
	UVPath        string // optional override (primarily for tests); defaults to EnsureUV
}

// InstallEvent reports stage updates and streaming stdout/stderr lines during installation.
type InstallEvent struct {
	Stage   string `json:"stage"` // bootstrapping_uv, creating_venv, installing_packages, verifying, done
	Message string `json:"message,omitempty"`
	LogLine string `json:"log_line,omitempty"`
}

// InstallResult describes the resulting Python virtual environment.
type InstallResult struct {
	VenvDir    string          `json:"venv_dir"`
	PythonPath string          `json:"python_path"`
	Candidate  PythonCandidate `json:"candidate"`
	UVPath     string          `json:"uv_path,omitempty"`
}

var defaultCADPackages = []string{
	"build123d>=0.8,<1.0",
	"ocp_tessellate>=3.0,<4.0",
}

// InstallEnvironment provisions Studio's shared managed virtual environment (or TargetVenvDir)
// using uv + managed Python 3.14 and installs build123d + ocp_tessellate (plus any library
// requirements.txt files), streaming live output via onEvent.
func InstallEnvironment(
	ctx context.Context,
	opts InstallOptions,
	onEvent func(InstallEvent),
) (InstallResult, error) {
	venvDir := ExpandPath(opts.TargetVenvDir)
	if venvDir == "" {
		defaultVenv, err := config.StudioVenvDir()
		if err != nil {
			return InstallResult{}, fmt.Errorf("resolve studio venv directory: %w", err)
		}
		venvDir = defaultVenv
	}
	if err := os.MkdirAll(filepath.Dir(venvDir), 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("create venv parent directory: %w", err)
	}

	workDir := filepath.Dir(venvDir)
	emit := func(ev InstallEvent) {
		if onEvent != nil {
			onEvent(ev)
		}
	}

	// Step 0: Ensure uv is available (reuse existing uv or download pinned release).
	uvPath := ExpandPath(opts.UVPath)
	if uvPath == "" {
		var err error
		uvPath, _, err = EnsureUV(ctx, emit)
		if err != nil {
			return InstallResult{}, fmt.Errorf("ensure uv: %w", err)
		}
	}

	// Step 1: Validate existing virtual environment before reuse, or create a clean one.
	venvPython := VenvPythonPath(venvDir)
	needCreate := true
	if _, err := os.Stat(venvPython); err == nil {
		emit(InstallEvent{
			Stage:   "creating_venv",
			Message: fmt.Sprintf("Validating existing virtual environment at %s...", venvDir),
		})
		existing := ProbeExecutable(ctx, venvPython, "studio_venv", "")
		if existing.Error == "" && existing.VersionOK {
			needCreate = false
			emit(InstallEvent{
				Stage:   "creating_venv",
				Message: fmt.Sprintf("Using existing virtual environment at %s...", venvDir),
			})
		} else {
			emit(InstallEvent{
				Stage:   "creating_venv",
				Message: fmt.Sprintf("Existing virtual environment at %s is broken or incomplete; recreating...", venvDir),
			})
			if rerr := os.RemoveAll(venvDir); rerr != nil {
				return InstallResult{}, fmt.Errorf("remove broken virtual environment: %w", rerr)
			}
		}
	}

	if needCreate {
		emit(InstallEvent{
			Stage:   "creating_venv",
			Message: fmt.Sprintf("Creating shared Studio virtual environment (Python %s) at %s...", ManagedPythonVersion, venvDir),
		})
		args := []string{
			"venv",
			"--python", ManagedPythonVersion,
			"--python-preference", "only-managed",
			venvDir,
		}
		if err := runStreamingCmd(ctx, workDir, uvPath, args, "creating_venv", emit); err != nil {
			return InstallResult{}, fmt.Errorf("create virtual environment with uv: %w", err)
		}
	}

	if _, err := os.Stat(venvPython); err != nil {
		return InstallResult{}, fmt.Errorf("virtual environment python not found at %s", venvPython)
	}

	// Step 2: Install pinned build123d + ocp_tessellate ranges
	emit(InstallEvent{
		Stage:   "installing_packages",
		Message: "Installing build123d and ocp_tessellate (this may take a few minutes)...",
	})

	installArgs := append([]string{"pip", "install", "--python", venvPython}, defaultCADPackages...)
	if err := runStreamingCmd(ctx, workDir, uvPath, installArgs, "installing_packages", emit); err != nil {
		return InstallResult{}, fmt.Errorf("install packages with uv: %w", err)
	}

	// Optional: install requirements.txt from any configured library
	for _, reqFile := range FindRequirementsFiles(opts.LibraryRoots) {
		libName := filepath.Base(filepath.Dir(reqFile))
		emit(InstallEvent{
			Stage:   "installing_packages",
			Message: fmt.Sprintf("Installing dependencies from %s/requirements.txt...", libName),
		})
		reqWorkDir := filepath.Dir(reqFile)
		args := []string{"pip", "install", "--python", venvPython, "-r", reqFile}
		if err := runStreamingCmd(ctx, reqWorkDir, uvPath, args, "installing_packages", emit); err != nil {
			return InstallResult{}, fmt.Errorf("install %s/requirements.txt with uv: %w", libName, err)
		}
	}

	// Step 3: Verify installation
	emit(InstallEvent{
		Stage:   "verifying",
		Message: "Verifying build123d installation...",
	})
	cand := ProbeExecutable(ctx, venvPython, "studio_venv", "")
	if cand.Error != "" {
		return InstallResult{}, fmt.Errorf("verify installed python: %s", cand.Error)
	}
	if !cand.HasBuild123d {
		return InstallResult{}, errors.New("installation completed, but build123d was not detected in the new virtual environment")
	}

	emit(InstallEvent{
		Stage:   "done",
		Message: "Python environment is ready!",
	})

	return InstallResult{
		VenvDir:    venvDir,
		PythonPath: venvPython,
		Candidate:  cand,
		UVPath:     uvPath,
	}, nil
}

func runStreamingCmd(
	ctx context.Context,
	workDir string,
	binary string,
	args []string,
	stage string,
	emit func(InstallEvent),
) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	configureCmd(cmd)
	if workDir != "" {
		if st, err := os.Stat(workDir); err == nil && st.IsDir() {
			cmd.Dir = workDir
		}
	}
	// Encourage unbuffered output from Python/pip/uv
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "PIP_NO_INPUT=1")

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return err
	}

	var lastLines []string
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
		scanner.Split(scanLinesOrCR)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			lastLines = append(lastLines, line)
			if len(lastLines) > 12 {
				lastLines = lastLines[len(lastLines)-12:]
			}
			emit(InstallEvent{
				Stage:   stage,
				LogLine: line,
			})
		}
		_, _ = io.Copy(io.Discard, pr)
	}()

	waitErr := cmd.Wait()
	_ = pw.Close()
	<-readDone
	_ = pr.Close()

	if waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(lastLines) > 0 {
			return fmt.Errorf("%w: %s", waitErr, lastLines[len(lastLines)-1])
		}
		return waitErr
	}
	return nil
}

func scanLinesOrCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
