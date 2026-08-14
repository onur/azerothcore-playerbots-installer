package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLContent(t *testing.T) {
	if len(createMySQLSQL) == 0 {
		t.Fatal("createMySQLSQL should not be empty")
	}

	requiredSubstrings := []string{
		"SET GLOBAL innodb_redo_log_capacity = 2 * 1024 * 1024 * 1024;",
		"SET GLOBAL innodb_io_capacity = 2000;",
		"SET GLOBAL innodb_io_capacity_max = 4000;",
		"CREATE USER IF NOT EXISTS 'acore'@'localhost'",
		"CREATE DATABASE IF NOT EXISTS `acore_world`",
		"CREATE DATABASE IF NOT EXISTS `acore_characters`",
		"CREATE DATABASE IF NOT EXISTS `acore_auth`",
		"CREATE DATABASE IF NOT EXISTS `acore_playerbots`",
		"GRANT ALL PRIVILEGES ON `acore_world`",
		"GRANT ALL PRIVILEGES ON `acore_playerbots`",
	}

	for _, sub := range requiredSubstrings {
		if !strings.Contains(createMySQLSQL, sub) {
			t.Errorf("createMySQLSQL missing expected string: %s", sub)
		}
	}
}

func TestParseArgsDefaults(t *testing.T) {
	opts, err := parseArgs([]string{})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	if opts.port != 3306 {
		t.Errorf("port = %d, want 3306", opts.port)
	}

	if opts.authPort != 3724 {
		t.Errorf("authPort = %d, want 3724", opts.authPort)
	}

	if opts.timeout != 30 {
		t.Errorf("timeout = %d, want 30", opts.timeout)
	}

	if opts.initOnly != false {
		t.Errorf("initOnly = %v, want false", opts.initOnly)
	}

	if opts.skipSQL != false {
		t.Errorf("skipSQL = %v, want false", opts.skipSQL)
	}
}

func TestParseArgsCustomFlags(t *testing.T) {
	customDir := t.TempDir()
	customData := filepath.Join(customDir, "mydata")

	args := []string{
		"-mysql-dir", customDir,
		"-data-dir", customData,
		"-port", "3307",
		"-auth-port", "3725",
		"-timeout", "45",
		"-init-only",
		"-skip-sql",
	}

	opts, err := parseArgs(args)
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	if opts.mysqlDir != customDir {
		t.Errorf("mysqlDir = %s, want %s", opts.mysqlDir, customDir)
	}

	if opts.dataDir != customData {
		t.Errorf("dataDir = %s, want %s", opts.dataDir, customData)
	}

	if opts.port != 3307 {
		t.Errorf("port = %d, want 3307", opts.port)
	}

	if opts.authPort != 3725 {
		t.Errorf("authPort = %d, want 3725", opts.authPort)
	}

	if opts.timeout != 45 {
		t.Errorf("timeout = %d, want 45", opts.timeout)
	}

	if !opts.initOnly {
		t.Errorf("initOnly = false, want true")
	}

	if !opts.skipSQL {
		t.Errorf("skipSQL = false, want true")
	}
}

func TestIsDataDirInitialized(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Non-existent directory
	nonExistent := filepath.Join(tmpDir, "does_not_exist")
	if isDataDirInitialized(nonExistent) {
		t.Errorf("isDataDirInitialized on non-existent dir should be false")
	}

	// 2. Empty directory
	emptyDir := filepath.Join(tmpDir, "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatalf("failed to create empty dir: %v", err)
	}
	if isDataDirInitialized(emptyDir) {
		t.Errorf("isDataDirInitialized on empty dir should be false")
	}

	// 3. Initialized directory with 'mysql' subfolder
	mysqlDataDir := filepath.Join(tmpDir, "initialized_mysql")
	if err := os.MkdirAll(filepath.Join(mysqlDataDir, "mysql"), 0755); err != nil {
		t.Fatalf("failed to create mysql subfolder: %v", err)
	}
	if !isDataDirInitialized(mysqlDataDir) {
		t.Errorf("isDataDirInitialized with mysql folder should be true")
	}

	// 4. Initialized directory with 'ibdata1' file
	ibdataDir := filepath.Join(tmpDir, "initialized_ibdata")
	if err := os.MkdirAll(ibdataDir, 0755); err != nil {
		t.Fatalf("failed to create ibdata dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ibdataDir, "ibdata1"), []byte("data"), 0644); err != nil {
		t.Fatalf("failed to write ibdata1: %v", err)
	}
	if !isDataDirInitialized(ibdataDir) {
		t.Errorf("isDataDirInitialized with ibdata1 should be true")
	}
}

func TestFindMySQLBinaries(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	// Create dummy binaries
	for _, name := range []string{"mysqld.exe", "mysql.exe", "mysqladmin.exe"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("bin"), 0755); err != nil {
			t.Fatalf("failed to write dummy binary %s: %v", name, err)
		}
	}

	binaries, err := findMySQLBinaries(tmpDir)
	if err != nil {
		t.Fatalf("findMySQLBinaries failed: %v", err)
	}

	if binaries.mysqld == "" || binaries.mysql == "" || binaries.mysqladmin == "" {
		t.Errorf("missing binaries: %+v", binaries)
	}
}

func TestFindMySQLBinariesMissing(t *testing.T) {
	emptyDir := t.TempDir()
	_, err := findMySQLBinaries(emptyDir)
	// If not in system PATH, should return error
	// Even if in PATH, let's verify calling with an empty dir doesn't panic
	if err != nil && !strings.Contains(err.Error(), "required MySQL binaries not found") {
		t.Errorf("unexpected error format: %v", err)
	}
}

func TestDirExistsAndFileExists(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	if !dirExists(tmpDir) {
		t.Errorf("dirExists(%s) = false, want true", tmpDir)
	}
	if dirExists(testFile) {
		t.Errorf("dirExists(%s) = true, want false", testFile)
	}
	if dirExists("") {
		t.Errorf("dirExists(\"\") = true, want false")
	}

	if !fileExists(testFile) {
		t.Errorf("fileExists(%s) = false, want true", testFile)
	}
	if fileExists(tmpDir) {
		t.Errorf("fileExists(%s) = true, want false", tmpDir)
	}
	if fileExists("") {
		t.Errorf("fileExists(\"\") = true, want false")
	}
}
