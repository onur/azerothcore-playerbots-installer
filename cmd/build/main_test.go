package main

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseArgs(t *testing.T) {
	origArgs := os.Args
	origCommandLine := flag.CommandLine
	defer func() {
		os.Args = origArgs
		flag.CommandLine = origCommandLine
	}()

	numCPU := runtime.NumCPU()

	tests := []struct {
		name           string
		args           []string
		wantConfig     string
		wantJobs       int
		wantMySQLDir   string
		wantBoostDir   string
		wantOpenSSLDir string
	}{
		{
			name:       "default values",
			args:       []string{"cmd"},
			wantConfig: "RelWithDebInfo",
			wantJobs:   numCPU,
		},
		{
			name:       "short flags",
			args:       []string{"cmd", "-c", "Release", "-j", "4"},
			wantConfig: "Release",
			wantJobs:   4,
		},
		{
			name:           "long flags with custom directories",
			args:           []string{"cmd", "--config", "Debug", "--jobs", "8", "--mysql-dir", "/opt/mysql", "--boost-dir", "/opt/boost", "--openssl-dir", "/opt/openssl"},
			wantConfig:     "Debug",
			wantJobs:       8,
			wantMySQLDir:   "/opt/mysql",
			wantBoostDir:   "/opt/boost",
			wantOpenSSLDir: "/opt/openssl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Args = tt.args
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
			opts := parseArgs()

			if opts.config != tt.wantConfig {
				t.Errorf("config = %v, want %v", opts.config, tt.wantConfig)
			}
			if opts.jobs != tt.wantJobs {
				t.Errorf("jobs = %v, want %v", opts.jobs, tt.wantJobs)
			}
			if tt.wantMySQLDir != "" && opts.mysqlDir != tt.wantMySQLDir {
				t.Errorf("mysqlDir = %v, want %v", opts.mysqlDir, tt.wantMySQLDir)
			}
			if tt.wantBoostDir != "" && opts.boostDir != tt.wantBoostDir {
				t.Errorf("boostDir = %v, want %v", opts.boostDir, tt.wantBoostDir)
			}
			if tt.wantOpenSSLDir != "" && opts.opensslDir != tt.wantOpenSSLDir {
				t.Errorf("opensslDir = %v, want %v", opts.opensslDir, tt.wantOpenSSLDir)
			}
		})
	}
}

func TestCopyWindowsDLLsMissingDirs(t *testing.T) {
	err := copyWindowsDLLs("", "some-openssl", "dist")
	if err == nil {
		t.Errorf("expected error when mysqlDir is empty, got nil")
	}

	err = copyWindowsDLLs("some-mysql", "", "dist")
	if err == nil {
		t.Errorf("expected error when opensslDir is empty, got nil")
	}
}

func TestRemoveDistExtensions(t *testing.T) {
	// Test non-existent directory
	if err := removeDistExtensions("non_existent_dir_12345"); err != nil {
		t.Fatalf("expected nil for non-existent directory, got %v", err)
	}

	// Setup temp test directory structure
	tempDir := t.TempDir()

	authDist := filepath.Join(tempDir, "authserver.conf.dist")
	worldDist := filepath.Join(tempDir, "worldserver.conf.dist")
	keepFile := filepath.Join(tempDir, "readme.txt")
	modulesDir := filepath.Join(tempDir, "modules")
	playerbotsDist := filepath.Join(modulesDir, "playerbots.conf.dist")

	if err := os.MkdirAll(modulesDir, 0755); err != nil {
		t.Fatalf("failed to create modules directory: %v", err)
	}

	if err := os.WriteFile(authDist, []byte("auth-content"), 0644); err != nil {
		t.Fatalf("failed to write authDist: %v", err)
	}
	if err := os.WriteFile(worldDist, []byte("world-content"), 0644); err != nil {
		t.Fatalf("failed to write worldDist: %v", err)
	}
	if err := os.WriteFile(keepFile, []byte("keep-content"), 0644); err != nil {
		t.Fatalf("failed to write keepFile: %v", err)
	}
	if err := os.WriteFile(playerbotsDist, []byte("playerbots-content"), 0644); err != nil {
		t.Fatalf("failed to write playerbotsDist: %v", err)
	}

	// Pre-create an existing target file to test overwrite
	existingAuth := filepath.Join(tempDir, "authserver.conf")
	if err := os.WriteFile(existingAuth, []byte("old-auth-content"), 0644); err != nil {
		t.Fatalf("failed to write existingAuth: %v", err)
	}

	// Run removeDistExtensions
	if err := removeDistExtensions(tempDir); err != nil {
		t.Fatalf("removeDistExtensions failed: %v", err)
	}

	// Check renamed files
	checkFile := func(path string, expectedContent string, shouldExist bool) {
		t.Helper()
		data, err := os.ReadFile(path)
		if shouldExist {
			if err != nil {
				t.Errorf("expected file %s to exist, but got error: %v", path, err)
			} else if string(data) != expectedContent {
				t.Errorf("file %s content = %q, want %q", path, string(data), expectedContent)
			}
		} else {
			if err == nil {
				t.Errorf("expected file %s to not exist, but it exists", path)
			}
		}
	}

	checkFile(authDist, "", false)
	checkFile(worldDist, "", false)
	checkFile(playerbotsDist, "", false)

	checkFile(filepath.Join(tempDir, "authserver.conf"), "auth-content", true)
	checkFile(filepath.Join(tempDir, "worldserver.conf"), "world-content", true)
	checkFile(filepath.Join(modulesDir, "playerbots.conf"), "playerbots-content", true)
	checkFile(keepFile, "keep-content", true)
}
