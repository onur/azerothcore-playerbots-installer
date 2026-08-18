package main

import (
	"archive/zip"
	"context"
	"net/http"
	"net/http/httptest"
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
	// 1. Mark and stop game server first while db is fully active
	gameSuper.MarkStopped()
	gameSuper.StopAndWait(1 * time.Second)
	recordEvent("game_stopped")

	// 2. Shut down db server last
	dbSuper.MarkStopped()
	cancel()
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

	// 1. Create root level .conf.dist with SourceDirectory, MySQLExecutable, DataDir, and BindIP
	worldDistContent := `
SourceDirectory = ""
MySQLExecutable = ""
DataDir = "."
BindIP = "0.0.0.0"
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
	if err := ensureConfigFiles(baseDir, baseDir, mysqlExe); err != nil {
		t.Fatalf("ensureConfigFiles failed: %v", err)
	}

	// Verify worldserver.conf created with replacements
	worldConfBytes, err := os.ReadFile(filepath.Join(configDir, "worldserver.conf"))
	if err != nil {
		t.Fatalf("failed to read created worldserver.conf: %v", err)
	}
	worldConf := string(worldConfBytes)
	if !strings.Contains(worldConf, `SourceDirectory = "src"`) {
		t.Errorf("worldserver.conf missing SourceDirectory = \"src\": %s", worldConf)
	}
	if !strings.Contains(worldConf, `MySQLExecutable = "mysql/bin/mysql.exe"`) {
		t.Errorf("worldserver.conf missing MySQLExecutable = \"mysql/bin/mysql.exe\": %s", worldConf)
	}
	if !strings.Contains(worldConf, `DataDir = "data"`) {
		t.Errorf("worldserver.conf missing DataDir = \"data\": %s", worldConf)
	}
	if !strings.Contains(worldConf, `BindIP = "127.0.0.1"`) {
		t.Errorf("worldserver.conf missing BindIP = \"127.0.0.1\": %s", worldConf)
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

	// Verify my.cnf was created in mysql directory with loopback
	myCnfBytes, err := os.ReadFile(filepath.Join(baseDir, "mysql", "my.cnf"))
	if err != nil {
		t.Fatalf("failed to read created mysql/my.cnf: %v", err)
	}
	myCnf := string(myCnfBytes)
	if !strings.Contains(myCnf, "bind-address = 127.0.0.1") {
		t.Errorf("my.cnf missing bind-address = 127.0.0.1: %s", myCnf)
	}
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

	// Verify .conf.dist templates remain preserved in baseDir
	if !fileExists(filepath.Join(configDir, "worldserver.conf.dist")) {
		t.Errorf("worldserver.conf.dist template should be preserved")
	}
	if !fileExists(filepath.Join(modulesDir, "playerbots.conf.dist")) {
		t.Errorf("playerbots.conf.dist template should be preserved")
	}
	if !fileExists(filepath.Join(configDir, "authserver.conf.dist")) {
		t.Errorf("authserver.conf.dist template should be preserved")
	}
}

func TestGetWorkDirEnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	envDir := filepath.Join(tmpDir, "custom_work")
	t.Setenv("PLAYERBOTS_WORKDIR", envDir)

	baseDir := filepath.Join(tmpDir, "base")
	_ = os.MkdirAll(baseDir, 0755)

	workDir := getWorkDir(baseDir)
	if workDir != envDir {
		t.Errorf("getWorkDir() = %s, want %s", workDir, envDir)
	}

	// Verify required subdirectories are created
	subdirs := []string{"configs", "logs", "data", "mysql", "mysql/data"}
	for _, sub := range subdirs {
		path := filepath.Join(envDir, sub)
		if !dirExists(path) {
			t.Errorf("expected directory to be created: %s", path)
		}
	}
}

func TestGetWorkDirPortable(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PLAYERBOTS_WORKDIR", "")

	baseDir := filepath.Join(tmpDir, "portable_app")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("failed to create baseDir: %v", err)
	}

	workDir := getWorkDir(baseDir)
	if workDir != baseDir {
		t.Errorf("getWorkDir() = %s, want %s for writable portable baseDir", workDir, baseDir)
	}

	// Verify subdirectories exist in baseDir
	subdirs := []string{"configs", "logs", "data", "mysql", "mysql/data"}
	for _, sub := range subdirs {
		path := filepath.Join(baseDir, sub)
		if !dirExists(path) {
			t.Errorf("expected directory to be created: %s", path)
		}
	}
}

func TestGetWorkDirLocalAppDataFallback(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PLAYERBOTS_WORKDIR", "")
	t.Setenv("LOCALAPPDATA", tmpDir)

	// Non-existent base directory simulates non-writable location
	nonWritableBaseDir := filepath.Join(tmpDir, "non_existent_or_readonly_base", "nested")

	workDir := getWorkDir(nonWritableBaseDir)
	expectedDir := filepath.Join(tmpDir, "Playerbots")
	if workDir != expectedDir {
		t.Errorf("getWorkDir() = %s, want %s", workDir, expectedDir)
	}

	// Verify logs and other subdirectories exist in LocalAppData/Playerbots
	subdirs := []string{"configs", "logs", "data", "mysql", "mysql/data"}
	for _, sub := range subdirs {
		path := filepath.Join(expectedDir, sub)
		if !dirExists(path) {
			t.Errorf("expected directory to be created: %s", path)
		}
	}
}

func TestIsDirWritable(t *testing.T) {
	tmpDir := t.TempDir()
	if !isDirWritable(tmpDir) {
		t.Errorf("expected isDirWritable(%s) = true", tmpDir)
	}

	nonExistent := filepath.Join(tmpDir, "does_not_exist")
	if isDirWritable(nonExistent) {
		t.Errorf("expected isDirWritable(%s) = false", nonExistent)
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
		"innodb_redo_log_capacity = 2G",
		"innodb_io_capacity = 1000",
		"innodb_io_capacity_max = 3000",
		"innodb_use_fdatasync = ON",
		"innodb_log_buffer_size = 64M",
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
	cnfPath, err := ensureMySQLConfigFile(tmpDir, tmpDir, mysqlDir)
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
	if !strings.Contains(string(contentBytes), "innodb_redo_log_capacity") {
		t.Errorf("my.cnf missing innodb_redo_log_capacity: %s", string(contentBytes))
	}

	// 2. Custom content should NOT be overwritten or modified on second run
	customContent := "[mysqld]\ninnodb_buffer_pool_size = 99G\ninnodb_redo_log_capacity = 4G\n"
	if err := os.WriteFile(cnfPath, []byte(customContent), 0644); err != nil {
		t.Fatalf("failed to write custom my.cnf: %v", err)
	}

	cnfPath2, err := ensureMySQLConfigFile(tmpDir, tmpDir, mysqlDir)
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
		t.Errorf("existing custom my.cnf was modified! Got: %s, Want: %s", string(reReadBytes), customContent)
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

func TestIsProcessAlive(t *testing.T) {
	// Current process PID should be alive
	alive, _ := isProcessAlive(os.Getpid())
	if !alive {
		t.Errorf("expected current process PID %d to be alive", os.Getpid())
	}

	// Invalid PID should not be alive
	alive, _ = isProcessAlive(-1)
	if alive {
		t.Errorf("expected PID -1 to not be alive")
	}
}

func TestWaitForAuthServerReadyProcessExit(t *testing.T) {
	// Helper process that immediately exits
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper process: %v", err)
	}

	// Wait for helper process to finish exiting
	_ = cmd.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := waitForAuthServerReady(ctx, cmd, 37240, 10*time.Second)
	duration := time.Since(start)

	if err == nil {
		t.Fatal("expected error when process exited, got nil")
	}

	if !strings.Contains(err.Error(), "exited prematurely") {
		t.Errorf("expected error message about premature exit, got: %v", err)
	}

	// Should fail quickly (within 2s), NOT waiting for the 10s timeout
	if duration > 2*time.Second {
		t.Errorf("detection took too long (%v), should have detected exit within ~500ms", duration)
	}
}

func TestParseArgsDataFlags(t *testing.T) {
	args := []string{
		"-skip-data-check",
		"-data-url", "https://example.com/custom-data.zip",
		"-download-data-only",
	}

	opts, err := parseArgs(args)
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	if !opts.skipDataCheck {
		t.Errorf("skipDataCheck = false, want true")
	}
	if opts.dataURL != "https://example.com/custom-data.zip" {
		t.Errorf("dataURL = %s, want https://example.com/custom-data.zip", opts.dataURL)
	}
	if !opts.downloadDataOnly {
		t.Errorf("downloadDataOnly = false, want true")
	}
}

func TestIsClientDataPresent(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Non-existent directory
	if isClientDataPresent(filepath.Join(tmpDir, "missing")) {
		t.Errorf("isClientDataPresent on non-existent dir should be false")
	}

	// 2. Empty directory
	emptyDir := filepath.Join(tmpDir, "empty")
	_ = os.MkdirAll(emptyDir, 0755)
	if isClientDataPresent(emptyDir) {
		t.Errorf("isClientDataPresent on empty dir should be false")
	}

	// 3. Incomplete subfolders (only dbc and maps)
	partialDir := filepath.Join(tmpDir, "partial")
	_ = os.MkdirAll(filepath.Join(partialDir, "dbc"), 0755)
	_ = os.WriteFile(filepath.Join(partialDir, "dbc", "test.dbc"), []byte("dbc"), 0644)
	_ = os.MkdirAll(filepath.Join(partialDir, "maps"), 0755)
	_ = os.WriteFile(filepath.Join(partialDir, "maps", "test.map"), []byte("map"), 0644)
	if isClientDataPresent(partialDir) {
		t.Errorf("isClientDataPresent with missing vmaps/mmaps should be false")
	}

	// 4. Subfolders exist but one is empty
	emptySubDir := filepath.Join(tmpDir, "empty_sub")
	for _, req := range []string{"dbc", "maps", "vmaps", "mmaps"} {
		_ = os.MkdirAll(filepath.Join(emptySubDir, req), 0755)
	}
	_ = os.WriteFile(filepath.Join(emptySubDir, "dbc", "test.dbc"), []byte("dbc"), 0644)
	_ = os.WriteFile(filepath.Join(emptySubDir, "maps", "test.map"), []byte("map"), 0644)
	_ = os.WriteFile(filepath.Join(emptySubDir, "vmaps", "test.vmtree"), []byte("vm"), 0644)
	// mmaps remains empty
	if isClientDataPresent(emptySubDir) {
		t.Errorf("isClientDataPresent with empty mmaps should be false")
	}

	// 5. Complete client data (all 4 have files)
	completeDir := filepath.Join(tmpDir, "complete")
	for _, req := range []string{"dbc", "maps", "vmaps", "mmaps"} {
		sub := filepath.Join(completeDir, req)
		_ = os.MkdirAll(sub, 0755)
		_ = os.WriteFile(filepath.Join(sub, "test.dat"), []byte("data"), 0644)
	}
	if !isClientDataPresent(completeDir) {
		t.Errorf("isClientDataPresent on complete data directory should be true")
	}
}

func createTestZip(t *testing.T, zipPath string, files map[string]string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(zipPath), 0755)
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("failed to create zip file: %v", err)
	}
	defer zipFile.Close()

	w := zip.NewWriter(zipFile)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write zip entry content for %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
}

func TestExtractZip(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")
	destDir := filepath.Join(tmpDir, "extracted")

	testFiles := map[string]string{
		"dbc/AreaTable.dbc":  "areatable_data",
		"maps/0004331.map":   "map_data",
		"vmaps/000.vmtree":   "vmtree_data",
		"mmaps/000.mmap":     "mmap_data",
		"cameras/camera.cam": "cam_data",
	}

	createTestZip(t, zipPath, testFiles)

	ctx := context.Background()
	if err := extractZip(ctx, zipPath, destDir); err != nil {
		t.Fatalf("extractZip failed: %v", err)
	}

	for relPath, expectedContent := range testFiles {
		fullPath := filepath.Join(destDir, relPath)
		if !fileExists(fullPath) {
			t.Errorf("expected extracted file %s does not exist", fullPath)
			continue
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			t.Errorf("failed to read %s: %v", fullPath, err)
			continue
		}
		if string(content) != expectedContent {
			t.Errorf("file %s content = %s, want %s", relPath, string(content), expectedContent)
		}
	}
}

func TestExtractZipWithDataPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test_prefix.zip")
	destDir := filepath.Join(tmpDir, "extracted_prefix")

	testFiles := map[string]string{
		"Data/dbc/AreaTable.dbc": "areatable_data",
		"Data/maps/0004331.map":  "map_data",
		"Data/vmaps/000.vmtree":  "vmtree_data",
		"Data/mmaps/000.mmap":    "mmap_data",
	}

	createTestZip(t, zipPath, testFiles)

	ctx := context.Background()
	if err := extractZip(ctx, zipPath, destDir); err != nil {
		t.Fatalf("extractZip with Data/ prefix failed: %v", err)
	}

	// Should be extracted into destDir/dbc, destDir/maps, etc. (prefix stripped)
	for _, req := range []string{"dbc/AreaTable.dbc", "maps/0004331.map", "vmaps/000.vmtree", "mmaps/000.mmap"} {
		fullPath := filepath.Join(destDir, req)
		if !fileExists(fullPath) {
			t.Errorf("expected file %s does not exist after extracting archive with Data/ prefix", fullPath)
		}
	}
}

func TestDownloadFileWithProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("mock_zip_binary_data_12345"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.bin")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := downloadFileWithProgress(ctx, server.URL, destPath); err != nil {
		t.Fatalf("downloadFileWithProgress failed: %v", err)
	}

	if !fileExists(destPath) {
		t.Fatalf("downloaded file %s does not exist", destPath)
	}

	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed reading downloaded file: %v", err)
	}

	if string(content) != "mock_zip_binary_data_12345" {
		t.Errorf("downloaded content = %s, want mock_zip_binary_data_12345", string(content))
	}
}

func TestEnsureClientData(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	baseDir := filepath.Join(tmpDir, "base")
	_ = os.MkdirAll(workDir, 0755)
	_ = os.MkdirAll(baseDir, 0755)

	ctx := context.Background()

	// 1. skipCheck = true -> should do nothing and return nil
	if err := ensureClientData(ctx, workDir, baseDir, "", true); err != nil {
		t.Errorf("ensureClientData with skipCheck=true failed: %v", err)
	}

	// 2. Client data already exists in baseDir -> should return nil without downloading
	baseDataDir := filepath.Join(baseDir, "data")
	for _, req := range []string{"dbc", "maps", "vmaps", "mmaps"} {
		sub := filepath.Join(baseDataDir, req)
		_ = os.MkdirAll(sub, 0755)
		_ = os.WriteFile(filepath.Join(sub, "test.dat"), []byte("data"), 0644)
	}
	if err := ensureClientData(ctx, workDir, baseDir, "", false); err != nil {
		t.Errorf("ensureClientData with existing baseDir client data failed: %v", err)
	}

	// 3. Missing client data -> download from mock server and extract
	freshWorkDir := filepath.Join(tmpDir, "fresh_work")
	freshBaseDir := filepath.Join(tmpDir, "fresh_base")

	// Create valid mock Data.zip
	mockZipPath := filepath.Join(tmpDir, "mock_Data.zip")
	testFiles := map[string]string{
		"dbc/AreaTable.dbc": "areatable_data",
		"maps/0004331.map":  "map_data",
		"vmaps/000.vmtree":  "vmtree_data",
		"mmaps/000.mmap":    "mmap_data",
	}
	createTestZip(t, mockZipPath, testFiles)
	mockZipBytes, err := os.ReadFile(mockZipPath)
	if err != nil {
		t.Fatalf("failed reading mock zip: %v", err)
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(mockZipBytes)
	}))
	defer mockServer.Close()

	if err := ensureClientData(ctx, freshWorkDir, freshBaseDir, mockServer.URL, false); err != nil {
		t.Fatalf("ensureClientData download + extract failed: %v", err)
	}

	// Verify client data is now present in freshWorkDir/data
	if !isClientDataPresent(filepath.Join(freshWorkDir, "data")) {
		t.Errorf("client data was not properly installed into %s", filepath.Join(freshWorkDir, "data"))
	}
}

