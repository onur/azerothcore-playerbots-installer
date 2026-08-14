package main

import (
	"flag"
	"os"
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
