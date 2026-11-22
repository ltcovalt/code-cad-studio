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
)

// InstallOptions configures automated virtual environment creation and package setup.
type InstallOptions struct {
	ProjectRoot   string
	BasePython    string
	TargetVenvDir string // defaults to <ProjectRoot>/.venv
	UseUV         bool
}

// InstallEvent reports stage updates and streaming stdout/stderr lines during installation.
type InstallEvent struct {
	Stage   string `json:"stage"` // creating_venv, installing_packages, verifying, done
	Message string `json:"message,omitempty"`
	LogLine string `json:"log_line,omitempty"`
}

// InstallResult describes the resulting Python virtual environment.
type InstallResult struct {
	VenvDir    string          `json:"venv_dir"`
	PythonPath string          `json:"python_path"`
	Candidate  PythonCandidate `json:"candidate"`
	UsedUV     bool            `json:"used_uv"`
}

// InstallEnvironment creates <ProjectRoot>/.venv and installs build123d + ocp_tessellate
// (plus project requirements.txt if present), streaming live output via onEvent.
func InstallEnvironment(
	ctx context.Context,
	opts InstallOptions,
	onEvent func(InstallEvent),
) (InstallResult, error) {
	projectRoot := ExpandPath(opts.ProjectRoot)
	if projectRoot == "" {
		return InstallResult{}, errors.New("project directory is required")
	}

	venvDir := ExpandPath(opts.TargetVenvDir)
	if venvDir == "" {
		venvDir = filepath.Join(projectRoot, ".venv")
	}
	if err := os.MkdirAll(filepath.Dir(venvDir), 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("create project directory: %w", err)
	}

	emit := func(ev InstallEvent) {
		if onEvent != nil {
			onEvent(ev)
		}
	}

	uvPath := ""
	if opts.UseUV {
		if p, err := exec.LookPath("uv"); err == nil && p != "" {
			uvPath = p
		}
	}
	basePython := ExpandPath(opts.BasePython)
	if uvPath == "" && basePython == "" {
		return InstallResult{}, errors.New("a Python 3.10+ interpreter is required to create a virtual environment")
	}

	// Step 1: Create virtual environment (reuse if already valid)
	venvPython := VenvPythonPath(venvDir)
	if _, err := os.Stat(venvPython); err != nil {
		emit(InstallEvent{
			Stage:   "creating_venv",
			Message: fmt.Sprintf("Creating virtual environment at %s...", venvDir),
		})
		if uvPath != "" {
			args := []string{"venv", venvDir}
			if basePython != "" {
				args = append(args, "--python", basePython)
			}
			if err := runStreamingCmd(ctx, projectRoot, uvPath, args, "creating_venv", emit); err != nil {
				return InstallResult{}, fmt.Errorf("create virtual environment with uv: %w", err)
			}
		} else {
			if err := runStreamingCmd(ctx, projectRoot, basePython, []string{"-m", "venv", venvDir}, "creating_venv", emit); err != nil {
				return InstallResult{}, fmt.Errorf("create virtual environment: %w", err)
			}
		}
	} else {
		emit(InstallEvent{
			Stage:   "creating_venv",
			Message: fmt.Sprintf("Using existing virtual environment at %s...", venvDir),
		})
	}

	if _, err := os.Stat(venvPython); err != nil {
		return InstallResult{}, fmt.Errorf("virtual environment python not found at %s", venvPython)
	}

	// Step 2: Install build123d + ocp_tessellate
	emit(InstallEvent{
		Stage:   "installing_packages",
		Message: "Installing build123d and ocp_tessellate (this may take a few minutes)...",
	})

	packages := []string{"build123d", "ocp_tessellate"}
	if uvPath != "" {
		args := append([]string{"pip", "install", "--python", venvPython}, packages...)
		if err := runStreamingCmd(ctx, projectRoot, uvPath, args, "installing_packages", emit); err != nil {
			return InstallResult{}, fmt.Errorf("install packages with uv: %w", err)
		}
	} else {
		args := append([]string{"-m", "pip", "install", "--disable-pip-version-check", "--progress-bar", "off"}, packages...)
		if err := runStreamingCmd(ctx, projectRoot, venvPython, args, "installing_packages", emit); err != nil {
			return InstallResult{}, fmt.Errorf("install packages with pip: %w", err)
		}
	}

	// Optional: install project requirements.txt if present
	reqFile := filepath.Join(projectRoot, "requirements.txt")
	if st, err := os.Stat(reqFile); err == nil && !st.IsDir() {
		emit(InstallEvent{
			Stage:   "installing_packages",
			Message: "Installing additional dependencies from requirements.txt...",
		})
		if uvPath != "" {
			args := []string{"pip", "install", "--python", venvPython, "-r", reqFile}
			if err := runStreamingCmd(ctx, projectRoot, uvPath, args, "installing_packages", emit); err != nil {
				return InstallResult{}, fmt.Errorf("install requirements.txt with uv: %w", err)
			}
		} else {
			args := []string{"-m", "pip", "install", "--disable-pip-version-check", "--progress-bar", "off", "-r", reqFile}
			if err := runStreamingCmd(ctx, projectRoot, venvPython, args, "installing_packages", emit); err != nil {
				return InstallResult{}, fmt.Errorf("install requirements.txt with pip: %w", err)
			}
		}
	}

	// Step 3: Verify installation
	emit(InstallEvent{
		Stage:   "verifying",
		Message: "Verifying build123d installation...",
	})
	cand := ProbeExecutable(ctx, venvPython, "project_venv")
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
		UsedUV:     uvPath != "",
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
