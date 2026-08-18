package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSQLContent(t *testing.T) {
	if len(createMySQLSQL) == 0 {
		t.Fatal("createMySQLSQL should not be empty")
	}

	requiredSubstrings := []string{
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
	customCnf := filepath.Join(customDir, "custom.cnf")

	args := []string{
		"-mysql-dir", customDir,
		"-data-dir", customData,
		"-mysql-cnf", customCnf,
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

	if opts.mysqlCnf != customCnf {
		t.Errorf("mysqlCnf = %s, want %s", opts.mysqlCnf, customCnf)
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

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	os.Exit(0)
}

func TestProcessSupervisorAutoRestart(t *testing.T) {
	var startCount atomic.Int32

	startFunc := func() (*exec.Cmd, error) {
		startCount.Add(1)
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}

	ps := newProcessSupervisor("test-service", startFunc, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go ps.Run(ctx, nil, &wg)

	// Wait until it has restarted at least 3 times
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if startCount.Load() >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if count := startCount.Load(); count < 3 {
		t.Errorf("expected at least 3 starts due to auto-restart, got %d", count)
	}

	// Cancel and ensure supervisor stops cleanly
	cancel()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Stopped cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop after context cancel")
	}
}

func TestProcessSupervisorStop(t *testing.T) {
	var startCount atomic.Int32

	startFunc := func() (*exec.Cmd, error) {
		startCount.Add(1)
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}

	ps := newProcessSupervisor("test-service-stop", startFunc, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go ps.Run(ctx, nil, &wg)

	// Wait for at least 1 start
	time.Sleep(100 * time.Millisecond)

	// Stop supervisor explicitly
	ps.Stop()
	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Stopped cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop after ps.Stop()")
	}

	countBefore := startCount.Load()
	time.Sleep(150 * time.Millisecond)
	countAfter := startCount.Load()

	if countAfter != countBefore {
		t.Errorf("supervisor continued starting processes after Stop(): %d -> %d", countBefore, countAfter)
	}
}

func TestOrderlyShutdown(t *testing.T) {
	var events []string
	var mu sync.Mutex

	recordEvent := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, name)
	}

	gameSuper := newProcessSupervisor("game", func() (*exec.Cmd, error) {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd, cmd.Start()
	}, 100*time.Millisecond)

	dbSuper := newProcessSupervisor("db", func() (*exec.Cmd, error) {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd, cmd.Start()
	}, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go gameSuper.Run(ctx, nil, nil)
	go dbSuper.Run(ctx, nil, nil)

	time.Sleep(50 * time.Millisecond)

	// Shutdown sequence:
	cancel()

	// 1. Stop game server first
	gameSuper.StopAndWait(1 * time.Second)
	recordEvent("game_stopped")

	// 2. Stop db server last
	dbSuper.StopAndWait(1 * time.Second)
	recordEvent("db_stopped")

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0] != "game_stopped" || events[1] != "db_stopped" {
		t.Errorf("unexpected shutdown order: %v", events)
	}
}

func TestEnsureConfigFiles(t *testing.T) {
	baseDir := t.TempDir()
	configDir := filepath.Join(baseDir, "configs")
	modulesDir := filepath.Join(configDir, "modules")
	if err := os.MkdirAll(modulesDir, 0755); err != nil {
		t.Fatalf("failed to create config dirs: %v", err)
	}

	// 1. Create root level .conf.dist with SourceDirectory and MySQLExecutable
	worldDistContent := `
SourceDirectory = ""
MySQLExecutable = ""
Rate.XP.Kill = 1
`
	if err := os.WriteFile(filepath.Join(configDir, "worldserver.conf.dist"), []byte(worldDistContent), 0644); err != nil {
		t.Fatalf("failed to write worldserver.conf.dist: %v", err)
	}

	// 2. Create module level .conf.dist (e.g. playerbots.conf.dist)
	playerbotsDistContent := `
Playerbots.Updates.EnableDatabases = 1
Playerbots.Debug.Enable = 0
AiPlayerbot.DisabledWithoutRealPlayer = 0
`
	if err := os.WriteFile(filepath.Join(modulesDir, "playerbots.conf.dist"), []byte(playerbotsDistContent), 0644); err != nil {
		t.Fatalf("failed to write playerbots.conf.dist: %v", err)
	}

	// 3. Create pre-existing custom config that should NOT be overwritten
	existingAuthContent := `
SourceDirectory = "custom/path"
MyCustomOption = 42
`
	if err := os.WriteFile(filepath.Join(configDir, "authserver.conf.dist"), []byte("SourceDirectory = \"\"\n"), 0644); err != nil {
		t.Fatalf("failed to write authserver.conf.dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "authserver.conf"), []byte(existingAuthContent), 0644); err != nil {
		t.Fatalf("failed to write authserver.conf: %v", err)
	}

	mysqlExe := filepath.Join(baseDir, "mysql", "bin", "mysql.exe")
	if err := ensureConfigFiles(baseDir, mysqlExe); err != nil {
		t.Fatalf("ensureConfigFiles failed: %v", err)
	}

	// Verify worldserver.conf created with replacements
	worldConfBytes, err := os.ReadFile(filepath.Join(configDir, "worldserver.conf"))
	if err != nil {
		t.Fatalf("failed to read created worldserver.conf: %v", err)
	}
	worldConf := string(worldConfBytes)
	if !strings.Contains(worldConf, `SourceDirectory = "."`) {
		t.Errorf("worldserver.conf missing SourceDirectory = \".\": %s", worldConf)
	}
	if !strings.Contains(worldConf, `MySQLExecutable = "mysql/bin/mysql.exe"`) {
		t.Errorf("worldserver.conf missing MySQLExecutable = \"mysql/bin/mysql.exe\": %s", worldConf)
	}
	if !strings.Contains(worldConf, `Rate.XP.Kill = 1`) {
		t.Errorf("worldserver.conf missing content: %s", worldConf)
	}

	// Verify modules/playerbots.conf created with DisabledWithoutRealPlayer = 1
	playerbotsConfBytes, err := os.ReadFile(filepath.Join(modulesDir, "playerbots.conf"))
	if err != nil {
		t.Fatalf("failed to read created playerbots.conf: %v", err)
	}
	playerbotsConf := string(playerbotsConfBytes)
	if !strings.Contains(playerbotsConf, "Playerbots.Updates.EnableDatabases = 1") {
		t.Errorf("playerbots.conf missing content: %s", playerbotsConf)
	}
	if !strings.Contains(playerbotsConf, "AiPlayerbot.DisabledWithoutRealPlayer = 1") {
		t.Errorf("playerbots.conf missing AiPlayerbot.DisabledWithoutRealPlayer = 1: %s", playerbotsConf)
	}

	// Verify my.cnf was created in mysql directory
	myCnfBytes, err := os.ReadFile(filepath.Join(baseDir, "mysql", "my.cnf"))
	if err != nil {
		t.Fatalf("failed to read created mysql/my.cnf: %v", err)
	}
	myCnf := string(myCnfBytes)
	if !strings.Contains(myCnf, "innodb_buffer_pool_size") {
		t.Errorf("my.cnf missing innodb_buffer_pool_size: %s", myCnf)
	}
	if !strings.Contains(myCnf, "skip-log-bin") {
		t.Errorf("my.cnf missing skip-log-bin: %s", myCnf)
	}

	// Verify pre-existing authserver.conf was not modified
	authConfBytes, err := os.ReadFile(filepath.Join(configDir, "authserver.conf"))
	if err != nil {
		t.Fatalf("failed to read authserver.conf: %v", err)
	}
	if string(authConfBytes) != existingAuthContent {
		t.Errorf("existing authserver.conf was overwritten! Got: %s, Want: %s", string(authConfBytes), existingAuthContent)
	}

	// Verify all .conf.dist files were removed
	if fileExists(filepath.Join(configDir, "worldserver.conf.dist")) {
		t.Errorf("worldserver.conf.dist was not removed")
	}
	if fileExists(filepath.Join(modulesDir, "playerbots.conf.dist")) {
		t.Errorf("playerbots.conf.dist was not removed")
	}
	if fileExists(filepath.Join(configDir, "authserver.conf.dist")) {
		t.Errorf("authserver.conf.dist was not removed")
	}
}

func TestCalculateMySQLBufferPoolSettings(t *testing.T) {
	poolSize, instances, _ := calculateMySQLBufferPoolSettings()
	if poolSize == "" {
		t.Error("poolSize should not be empty")
	}
	if instances < 1 {
		t.Errorf("instances should be >= 1, got %d", instances)
	}
}

func TestGenerateDefaultMyCnf(t *testing.T) {
	cnf := generateDefaultMyCnf("32G", 12, 64)

	requiredOptions := []string{
		"[mysqld]",
		"innodb_buffer_pool_size = 32G",
		"innodb_buffer_pool_instances = 12",
		"innodb_io_capacity = 500",
		"innodb_io_capacity_max = 2500",
		"innodb_use_fdatasync = ON",
		"innodb_log_buffer_size = 32M",
		"binlog_expire_logs_seconds = 432000",
		`transaction_isolation = "READ-COMMITTED"`,
		"skip-log-bin",
		"innodb_flush_log_at_trx_commit = 2",
		"max_connections = 500",
		"max_allowed_packet = 64M",
		"character-set-server = utf8mb4",
		"collation-server = utf8mb4_unicode_ci",
		"table_open_cache = 4000",
		"table_definition_cache = 2000",
		"[client]",
		"[mysql]",
	}

	for _, opt := range requiredOptions {
		if !strings.Contains(cnf, opt) {
			t.Errorf("generateDefaultMyCnf output missing expected line: %s", opt)
		}
	}
}

func TestEnsureMySQLConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	mysqlDir := filepath.Join(tmpDir, "mysql")

	// 1. First run creates my.cnf
	cnfPath, err := ensureMySQLConfigFile(tmpDir, mysqlDir)
	if err != nil {
		t.Fatalf("ensureMySQLConfigFile failed: %v", err)
	}

	if !fileExists(cnfPath) {
		t.Fatalf("my.cnf was not created at %s", cnfPath)
	}

	contentBytes, err := os.ReadFile(cnfPath)
	if err != nil {
		t.Fatalf("failed to read created my.cnf: %v", err)
	}
	if !strings.Contains(string(contentBytes), "innodb_buffer_pool_size") {
		t.Errorf("my.cnf content invalid: %s", string(contentBytes))
	}

	// 2. Custom content should NOT be overwritten on second run
	customContent := "[mysqld]\ninnodb_buffer_pool_size = 99G\n"
	if err := os.WriteFile(cnfPath, []byte(customContent), 0644); err != nil {
		t.Fatalf("failed to write custom my.cnf: %v", err)
	}

	cnfPath2, err := ensureMySQLConfigFile(tmpDir, mysqlDir)
	if err != nil {
		t.Fatalf("second ensureMySQLConfigFile failed: %v", err)
	}
	if cnfPath2 != cnfPath {
		t.Errorf("returned path mismatch: %s vs %s", cnfPath2, cnfPath)
	}

	reReadBytes, err := os.ReadFile(cnfPath)
	if err != nil {
		t.Fatalf("failed to re-read my.cnf: %v", err)
	}
	if string(reReadBytes) != customContent {
		t.Errorf("custom my.cnf was overwritten! Got: %s, Want: %s", string(reReadBytes), customContent)
	}
}

func TestFindMySQLConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	mysqlDir := filepath.Join(tmpDir, "mysql")
	if err := os.MkdirAll(mysqlDir, 0755); err != nil {
		t.Fatalf("failed to create mysql dir: %v", err)
	}

	// Nothing exists
	if found := findMySQLConfigFile("", mysqlDir, tmpDir); found != "" {
		t.Errorf("found should be empty when no file exists, got: %s", found)
	}

	// Create custom path
	customPath := filepath.Join(tmpDir, "custom.ini")
	if err := os.WriteFile(customPath, []byte("[mysqld]\n"), 0644); err != nil {
		t.Fatalf("failed to write custom.ini: %v", err)
	}

	if found := findMySQLConfigFile(customPath, mysqlDir, tmpDir); found != customPath {
		t.Errorf("expected custom path %s, got %s", customPath, found)
	}

	// Create mysqlDir/my.cnf
	myCnfPath := filepath.Join(mysqlDir, "my.cnf")
	if err := os.WriteFile(myCnfPath, []byte("[mysqld]\n"), 0644); err != nil {
		t.Fatalf("failed to write my.cnf: %v", err)
	}

	if found := findMySQLConfigFile("", mysqlDir, tmpDir); found != myCnfPath {
		t.Errorf("expected %s, got %s", myCnfPath, found)
	}
}



