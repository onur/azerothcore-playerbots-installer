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
	if numCPU < 1 {
		numCPU = 1
	}

	tests := []struct {
		name       string
		args       []string
		wantConfig string
		wantJobs   int
		wantClean  bool
	}{
		{
			name:       "default values",
			args:       []string{"cmd"},
			wantConfig: "RelWithDebInfo",
			wantJobs:   numCPU,
			wantClean:  false,
		},
		{
			name:       "short flags",
			args:       []string{"cmd", "-c", "Release", "-j", "4", "-clean"},
			wantConfig: "Release",
			wantJobs:   4,
			wantClean:  true,
		},
		{
			name:       "long flags with double dash",
			args:       []string{"cmd", "--config", "Debug", "--jobs", "8"},
			wantConfig: "Debug",
			wantJobs:   8,
			wantClean:  false,
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
			if opts.clean != tt.wantClean {
				t.Errorf("clean = %v, want %v", opts.clean, tt.wantClean)
			}
		})
	}
}
