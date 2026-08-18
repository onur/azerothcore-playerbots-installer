package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const createMySQLSQL = `
CREATE USER IF NOT EXISTS 'acore'@'localhost' IDENTIFIED BY 'acore' WITH MAX_QUERIES_PER_HOUR 0 MAX_CONNECTIONS_PER_HOUR 0 MAX_UPDATES_PER_HOUR 0;

CREATE DATABASE IF NOT EXISTS ` + "`acore_world`" + ` DEFAULT CHARACTER SET UTF8MB4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS ` + "`acore_characters`" + ` DEFAULT CHARACTER SET UTF8MB4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS ` + "`acore_auth`" + ` DEFAULT CHARACTER SET UTF8MB4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS ` + "`acore_playerbots`" + ` DEFAULT CHARACTER SET UTF8MB4 COLLATE utf8mb4_unicode_ci;

GRANT ALL PRIVILEGES ON ` + "`acore_world`" + ` . * TO 'acore'@'localhost' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON ` + "`acore_characters`" + ` . * TO 'acore'@'localhost' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON ` + "`acore_auth`" + ` . * TO 'acore'@'localhost' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON ` + "`acore_playerbots`" + ` . * TO 'acore'@'localhost' WITH GRANT OPTION;
`

type startupOptions struct {
	mysqlDir string
	dataDir  string
	mysqlCnf string
	port     int
	authPort int
	timeout  int
	initOnly bool
	skipSQL  bool
}

type mysqlBinaries struct {
	mysqld     string
	mysql      string
	mysqladmin string
	dir        string
}

type ProcessSupervisor struct {
	name         string
	startFunc    func() (*exec.Cmd, error)
	restartDelay time.Duration

	mu         sync.Mutex
	currentCmd *exec.Cmd
	stopped    bool
	doneChan   chan struct{}
}

func newProcessSupervisor(name string, startFunc func() (*exec.Cmd, error), restartDelay time.Duration) *ProcessSupervisor {
	return &ProcessSupervisor{
		name:         name,
		startFunc:    startFunc,
		restartDelay: restartDelay,
		doneChan:     make(chan struct{}),
	}
}

func (ps *ProcessSupervisor) SetCurrentCmd(cmd *exec.Cmd) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.currentCmd = cmd
}

func (ps *ProcessSupervisor) GetCurrentCmd() *exec.Cmd {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.currentCmd
}

func (ps *ProcessSupervisor) MarkStopped() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.stopped = true
}

func (ps *ProcessSupervisor) Kill() {
	ps.mu.Lock()
	ps.stopped = true
	cmd := ps.currentCmd
	ps.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		fmt.Printf("Stopping %s (PID: %d)...\n", ps.name, cmd.Process.Pid)
		_ = cmd.Process.Kill()
	}
}

func (ps *ProcessSupervisor) Stop() {
	ps.Kill()
}

func (ps *ProcessSupervisor) StopAndWait(gracePeriod time.Duration) {
	ps.MarkStopped()

	select {
	case <-ps.doneChan:
		return
	case <-time.After(gracePeriod):
		fmt.Printf("%s did not stop within %v, killing process...\n", ps.name, gracePeriod)
		ps.Kill()
		<-ps.doneChan
	}
}

func (ps *ProcessSupervisor) Run(ctx context.Context, initialCmd *exec.Cmd, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}
	defer close(ps.doneChan)

	cmd := initialCmd
	for {
		if cmd == nil {
			select {
			case <-ctx.Done():
				return
			default:
			}

			var err error
			cmd, err = ps.startFunc()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					fmt.Fprintf(os.Stderr, "Failed to start %s: %v\n", ps.name, err)
				}

				select {
				case <-ctx.Done():
					return
				case <-time.After(ps.restartDelay):
					continue
				}
			}
		}

		ps.mu.Lock()
		if ps.stopped || ctx.Err() != nil {
			ps.mu.Unlock()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			return
		}
		ps.currentCmd = cmd
		ps.mu.Unlock()

		err := cmd.Wait()

		ps.mu.Lock()
		ps.currentCmd = nil
		stopped := ps.stopped
		ps.mu.Unlock()

		if stopped || ctx.Err() != nil {
			return
		}

		if err != nil {
			fmt.Printf("\n[%s] Process exited: %v\n", ps.name, err)
		} else {
			fmt.Printf("\n[%s] Process stopped.\n", ps.name)
		}

		fmt.Printf("[%s] Restarting in %v...\n", ps.name, ps.restartDelay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(ps.restartDelay):
		}

		cmd = nil
	}
}

func dirExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func findDefaultMySQLDir() string {
	// 1. Check relative to executable directory: <exeDir>/mysql
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidate := filepath.Join(exeDir, "mysql")
		if dirExists(candidate) {
			return candidate
		}
	}

	// 2. Check current working directory: ./mysql
	if dirExists("mysql") {
		absPath, err := filepath.Abs("mysql")
		if err == nil {
			return absPath
		}
		return "mysql"
	}

	// 3. Check MYSQL_ROOT environment variable
	if envVal := os.Getenv("MYSQL_ROOT"); envVal != "" && dirExists(envVal) {
		return envVal
	}

	// 4. Check dist/mysql relative to cwd
	if dirExists(filepath.Join("dist", "mysql")) {
		absPath, err := filepath.Abs(filepath.Join("dist", "mysql"))
		if err == nil {
			return absPath
		}
		return filepath.Join("dist", "mysql")
	}

	// 5. Check deps/mysql-* directory
	depsPattern := filepath.Join("deps", "mysql-*")
	if matches, err := filepath.Glob(depsPattern); err == nil {
		for _, m := range matches {
			if dirExists(m) {
				absPath, err := filepath.Abs(m)
				if err == nil {
					return absPath
				}
				return m
			}
		}
	}

	return ""
}

func parseArgs(args []string) (startupOptions, error) {
	var opts startupOptions

	fs := flag.NewFlagSet("startup", flag.ContinueOnError)

	defaultMysql := findDefaultMySQLDir()

	fs.StringVar(&opts.mysqlDir, "mysql-dir", defaultMysql, "Path to MySQL root directory. Default: ./mysql or $env:MYSQL_ROOT")
	fs.StringVar(&opts.dataDir, "data-dir", "", "Path to MySQL data directory. Default: <mysql-dir>/data")
	fs.StringVar(&opts.mysqlCnf, "mysql-cnf", "", "Path to MySQL configuration file (my.cnf or my.ini). Default: auto-detected in <mysql-dir>")
	fs.IntVar(&opts.port, "port", 3306, "MySQL server port.")
	fs.IntVar(&opts.authPort, "auth-port", 3724, "Authserver realm port.")
	fs.IntVar(&opts.timeout, "timeout", 30, "Timeout in seconds to wait for MySQL to become ready.")
	fs.BoolVar(&opts.initOnly, "init-only", false, "Initialize MySQL data dir, start server, apply SQL script, and exit.")
	fs.BoolVar(&opts.skipSQL, "skip-sql", false, "Skip executing the embedded create_mysql.sql script.")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of mod-playerbots startup tool:\n\n")
		fmt.Fprintf(fs.Output(), "Start MySQL server in console mode and initialize AzerothCore database.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return opts, err
	}

	if opts.dataDir == "" && opts.mysqlDir != "" {
		opts.dataDir = filepath.Join(opts.mysqlDir, "data")
	}

	return opts, nil
}

func findExecutable(dir, name string) string {
	exts := []string{".exe", ".bat", ".cmd", ""}

	if dir != "" {
		for _, ext := range exts {
			fullPath := filepath.Join(dir, name+ext)
			if fileExists(fullPath) {
				return fullPath
			}
		}
	}

	for _, ext := range exts {
		if path, err := exec.LookPath(name + ext); err == nil {
			return path
		}
	}

	return ""
}

func findMySQLBinaries(mysqlDir string) (*mysqlBinaries, error) {
	binDir := ""
	if mysqlDir != "" {
		binDir = filepath.Join(mysqlDir, "bin")
		if !dirExists(binDir) {
			binDir = mysqlDir
		}
	}

	mysqld := findExecutable(binDir, "mysqld")
	mysql := findExecutable(binDir, "mysql")
	mysqladmin := findExecutable(binDir, "mysqladmin")

	var missing []string
	if mysqld == "" {
		missing = append(missing, "mysqld")
	}
	if mysql == "" {
		missing = append(missing, "mysql")
	}
	if mysqladmin == "" {
		missing = append(missing, "mysqladmin")
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("required MySQL binaries not found (%s) in %s or PATH", strings.Join(missing, ", "), binDir)
	}

	return &mysqlBinaries{
		mysqld:     mysqld,
		mysql:      mysql,
		mysqladmin: mysqladmin,
		dir:        mysqlDir,
	}, nil
}

func isDataDirInitialized(dataDir string) bool {
	if !dirExists(dataDir) {
		return false
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) == 0 {
		return false
	}

	return true
}

func initializeMySQL(binaries *mysqlBinaries, dataDir string, configFile string) error {
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute data dir: %w", err)
	}

	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory %s: %w", absDataDir, err)
	}

	fmt.Printf("=== [1/4] Initializing MySQL data directory at %s ===\n", absDataDir)

	var args []string
	if configFile != "" && fileExists(configFile) {
		if absCnf, err := filepath.Abs(configFile); err == nil {
			args = append(args, fmt.Sprintf("--defaults-file=%s", absCnf))
		} else {
			args = append(args, fmt.Sprintf("--defaults-file=%s", configFile))
		}
	}

	args = append(args,
		"--initialize-insecure",
		fmt.Sprintf("--datadir=%s", absDataDir),
		"--console",
	)

	cmd := exec.Command(binaries.mysqld, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mysqld --initialize-insecure failed: %w", err)
	}

	fmt.Println("MySQL data directory initialized successfully.")
	return nil
}

func startMySQLServer(binaries *mysqlBinaries, dataDir string, port int, configFile string) (*exec.Cmd, error) {
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute data dir: %w", err)
	}

	var args []string
	if configFile != "" && fileExists(configFile) {
		if absCnf, err := filepath.Abs(configFile); err == nil {
			args = append(args, fmt.Sprintf("--defaults-file=%s", absCnf))
		} else {
			args = append(args, fmt.Sprintf("--defaults-file=%s", configFile))
		}
	}

	args = append(args,
		fmt.Sprintf("--datadir=%s", absDataDir),
		fmt.Sprintf("--port=%d", port),
		"--console",
	)

	fmt.Printf("=== Starting MySQL server (Port: %d) ===\n", port)
	cmd := exec.Command(binaries.mysqld, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start mysqld: %w", err)
	}

	return cmd, nil
}

func waitForMySQLReady(binaries *mysqlBinaries, port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	fmt.Printf("Waiting for MySQL server to accept connections on port %d...\n", port)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for MySQL server after %v", timeout)
		case <-ticker.C:
			// Check TCP port connectivity
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
			if err != nil {
				continue
			}
			conn.Close()

			// Check via mysqladmin ping
			args := []string{
				"-u", "root",
				"-h", "127.0.0.1",
				"-P", fmt.Sprintf("%d", port),
				"--protocol=tcp",
				"ping",
			}
			pingCmd := exec.Command(binaries.mysqladmin, args...)
			if err := pingCmd.Run(); err == nil {
				fmt.Println("MySQL server is online and ready.")
				return nil
			}
		}
	}
}

func waitForAuthServerReady(port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	fmt.Printf("Waiting for authserver to initialize and listen on port %d...\n", port)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for authserver on port %d after %v", port, timeout)
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
			if err == nil {
				conn.Close()
				fmt.Println("Authserver is online and ready.")
				return nil
			}
		}
	}
}

func runEmbeddedSQL(binaries *mysqlBinaries, port int, sqlContent string) error {
	if strings.TrimSpace(sqlContent) == "" {
		return errors.New("embedded SQL content is empty")
	}

	fmt.Println("\n=== [3/4] Applying create_mysql.sql ===")

	args := []string{
		"-u", "root",
		"-h", "127.0.0.1",
		"-P", fmt.Sprintf("%d", port),
		"--protocol=tcp",
	}

	cmd := exec.Command(binaries.mysql, args...)
	cmd.Stdin = strings.NewReader(sqlContent)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to execute embedded SQL script: %w", err)
	}

	fmt.Println("AzerothCore databases and user 'acore' ready.")
	return nil
}

func startServerProcess(name, exePath, workDir string, stdin *os.File) (*exec.Cmd, error) {
	cmd := exec.Command(exePath)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if stdin != nil {
		cmd.Stdin = stdin
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", name, err)
	}

	fmt.Printf("%s started successfully (PID: %d).\n", name, cmd.Process.Pid)
	return cmd, nil
}

func stopProcess(name string, cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	fmt.Printf("Stopping %s (PID: %d)...\n", name, cmd.Process.Pid)
	_ = cmd.Process.Kill()
}

func shutdownMySQL(binaries *mysqlBinaries, port int) error {
	fmt.Println("Shutting down MySQL server...")

	args := []string{
		"-u", "root",
		"-h", "127.0.0.1",
		"-P", fmt.Sprintf("%d", port),
		"--protocol=tcp",
		"shutdown",
	}

	shutdownCmd := exec.Command(binaries.mysqladmin, args...)
	shutdownCmd.Stdout = os.Stdout
	shutdownCmd.Stderr = os.Stderr
	_ = shutdownCmd.Run()

	return nil
}

func shutdownMySQLAndWait(binaries *mysqlBinaries, port int, cmd *exec.Cmd) error {
	fmt.Println("Shutting down MySQL server...")

	args := []string{
		"-u", "root",
		"-h", "127.0.0.1",
		"-P", fmt.Sprintf("%d", port),
		"--protocol=tcp",
		"shutdown",
	}

	shutdownCmd := exec.Command(binaries.mysqladmin, args...)
	shutdownCmd.Stdout = os.Stdout
	shutdownCmd.Stderr = os.Stderr
	_ = shutdownCmd.Run()

	done := make(chan error, 1)
	go func() {
		if cmd != nil {
			done <- cmd.Wait()
		} else {
			done <- nil
		}
	}()

	select {
	case <-time.After(10 * time.Second):
		if cmd != nil && cmd.Process != nil {
			fmt.Println("MySQL did not stop within 10s, terminating process...")
			_ = cmd.Process.Kill()
		}
	case <-done:
		fmt.Println("MySQL server stopped cleanly.")
	}

	return nil
}

func findBaseDir() string {
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		if fileExists(filepath.Join(exeDir, "authserver.exe")) {
			return exeDir
		}
	}

	if fileExists("authserver.exe") {
		if abs, err := filepath.Abs("."); err == nil {
			return abs
		}
	}

	if fileExists(filepath.Join("dist", "authserver.exe")) {
		if abs, err := filepath.Abs("dist"); err == nil {
			return abs
		}
	}

	return "."
}

type memoryStatusEx struct {
	cbSize                  uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

func getTotalRAMBytes() uint64 {
	if runtime.GOOS != "windows" {
		return 0
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	globalMemoryStatusEx := kernel32.NewProc("GlobalMemoryStatusEx")
	var mem memoryStatusEx
	mem.cbSize = uint32(unsafe.Sizeof(mem))
	ret, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&mem)))
	if ret == 0 {
		return 0
	}
	return mem.ullTotalPhys
}

func calculateMySQLBufferPoolSettings() (sizeStr string, instances int, totalRAMGB int) {
	totalRAMBytes := getTotalRAMBytes()
	if totalRAMBytes == 0 {
		// Fallback default: 4G buffer pool, 4 instances
		return "4G", 4, 0
	}

	ramGB := int((totalRAMBytes + (512 * 1024 * 1024)) / (1024 * 1024 * 1024))
	poolGB := ramGB / 2 // 50% of total RAM

	if poolGB < 1 {
		return "512M", 1, ramGB
	}

	instances = poolGB / 2
	if instances < 1 {
		instances = 1
	} else if instances > 16 {
		instances = 16
	}

	// For specific high-RAM tiers, match recommended fine tuning values
	if poolGB >= 32 {
		instances = 12 // Recommended setting for 64GB RAM / 32G pool
	}

	return fmt.Sprintf("%dG", poolGB), instances, ramGB
}

func generateDefaultMyCnf(poolSize string, poolInstances int, totalRAMGB int) string {
	ramComment := ""
	if totalRAMGB > 0 {
		ramComment = fmt.Sprintf("# System RAM detected: ~%d GB (Buffer pool set to ~50%%: %s)\n", totalRAMGB, poolSize)
	}

	return fmt.Sprintf(`#
# MySQL / MariaDB Configuration for AzerothCore + mod-playerbots
#
# The default MySQL configuration is not adequate for use with Playerbots,
# and will lead to increased disk activity and decreased performance.
#
%s# Note: Buffer pool size should ideally be 50%% of your total RAM.
#

[mysqld]
# Basic Network and Connection Settings
port = 3306
max_connections = 500
max_allowed_packet = 64M

# Character Set
character-set-server = utf8mb4
collation-server = utf8mb4_unicode_ci

#
# * Fine Tuning for Playerbots
#

# INNODB Buffer Pool & I/O
innodb_buffer_pool_size = %s
innodb_buffer_pool_instances = %d
innodb_log_buffer_size = 32M
innodb_io_capacity = 500
innodb_io_capacity_max = 2500
innodb_use_fdatasync = ON
innodb_read_io_threads = 4
innodb_write_io_threads = 4

# Performance & SSD Lifespan Optimization:
# Flushes redo log to OS cache every commit and to disk once per second (massive write boost).
innodb_flush_log_at_trx_commit = 2

# Table Cache
table_open_cache = 4000
table_definition_cache = 2000

# Binary Logging:
# skip-log-bin reduces ~75-90%% of disk writes by skipping binary logging.
skip-log-bin

# Max age of binary logs if binary logging is re-enabled - 5 days to prevent binary log pileups
binlog_expire_logs_seconds = 432000

# Prevent SQL Deadlocks as much as possible
transaction_isolation = "READ-COMMITTED"

[client]
default-character-set = utf8mb4

[mysql]
default-character-set = utf8mb4
`, ramComment, poolSize, poolInstances)
}

func findMySQLConfigFile(customPath, mysqlDir, baseDir string) string {
	if customPath != "" && fileExists(customPath) {
		return customPath
	}

	candidates := []string{
		filepath.Join(mysqlDir, "my.cnf"),
		filepath.Join(mysqlDir, "my.ini"),
		filepath.Join(baseDir, "mysql", "my.cnf"),
		filepath.Join(baseDir, "mysql", "my.ini"),
		filepath.Join(baseDir, "configs", "my.cnf"),
		filepath.Join(baseDir, "configs", "my.ini"),
		filepath.Join(baseDir, "my.cnf"),
		filepath.Join(baseDir, "my.ini"),
	}

	for _, cand := range candidates {
		if cand != "" && fileExists(cand) {
			return cand
		}
	}

	return ""
}

func ensureMySQLConfigFile(baseDir, mysqlDir string) (string, error) {
	if existing := findMySQLConfigFile("", mysqlDir, baseDir); existing != "" {
		if data, err := os.ReadFile(existing); err == nil {
			content := string(data)
			if strings.Contains(content, "skip-name-resolve") {
				cleaned := strings.ReplaceAll(content, "skip-name-resolve\r\n", "")
				cleaned = strings.ReplaceAll(cleaned, "skip-name-resolve\n", "")
				cleaned = strings.ReplaceAll(cleaned, "skip-name-resolve", "")
				_ = os.WriteFile(existing, []byte(cleaned), 0644)
			}
		}
		return existing, nil
	}

	targetDir := mysqlDir
	if targetDir == "" {
		targetDir = filepath.Join(baseDir, "mysql")
	}

	targetPath := filepath.Join(targetDir, "my.cnf")

	poolSize, instances, ramGB := calculateMySQLBufferPoolSettings()
	content := generateDefaultMyCnf(poolSize, instances, ramGB)

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory for %s: %w", targetPath, err)
	}

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", targetPath, err)
	}

	relTarget, err := filepath.Rel(baseDir, targetPath)
	if err != nil {
		relTarget = targetPath
	}
	fmt.Printf("Created default MySQL config: %s (Buffer pool: %s, Instances: %d)\n", filepath.ToSlash(relTarget), poolSize, instances)

	return targetPath, nil
}

func ensureConfigFiles(baseDir string, mysqlExePath string, mysqlDir ...string) error {
	resolvedMySQLDir := ""
	if len(mysqlDir) > 0 && mysqlDir[0] != "" {
		resolvedMySQLDir = mysqlDir[0]
	} else if mysqlExePath != "" {
		resolvedMySQLDir = filepath.Dir(filepath.Dir(mysqlExePath))
	} else {
		resolvedMySQLDir = filepath.Join(baseDir, "mysql")
	}

	// 1. Ensure MySQL configuration file (my.cnf) exists with fine-tuning settings
	if _, err := ensureMySQLConfigFile(baseDir, resolvedMySQLDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure MySQL config file: %v\n", err)
	}

	// 2. Ensure server configs from .conf.dist in configs/
	configDir := filepath.Join(baseDir, "configs")
	if !dirExists(configDir) {
		return nil
	}

	relMySQLExe := "mysql/bin/mysql.exe"
	if mysqlExePath != "" {
		if rel, err := filepath.Rel(baseDir, mysqlExePath); err == nil {
			relMySQLExe = filepath.ToSlash(rel)
		}
	}

	return filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		if strings.HasSuffix(info.Name(), ".conf.dist") {
			targetConfPath := strings.TrimSuffix(path, ".dist")
			if !fileExists(targetConfPath) {
				data, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("failed to read %s: %w", path, err)
				}

				content := string(data)
				content = strings.Replace(content, `DataDir = "."`, `DataDir = "data"`, 1)
				content = strings.Replace(content, `SourceDirectory = ""`, `SourceDirectory = "src"`, 1)
				content = strings.Replace(content, `MySQLExecutable = ""`, fmt.Sprintf(`MySQLExecutable = "%s"`, relMySQLExe), 1)

				// Enable AiPlayerbot.DisabledWithoutRealPlayer by default to reduce disk writes when no real players are online
				if strings.Contains(info.Name(), "playerbots") {
					content = strings.Replace(content, "AiPlayerbot.DisabledWithoutRealPlayer = 0", "AiPlayerbot.DisabledWithoutRealPlayer = 1", 1)
				}

				if err := os.MkdirAll(filepath.Dir(targetConfPath), 0755); err != nil {
					return fmt.Errorf("failed to create directory for %s: %w", targetConfPath, err)
				}

				if err := os.WriteFile(targetConfPath, []byte(content), 0644); err != nil {
					return fmt.Errorf("failed to write %s: %w", targetConfPath, err)
				}

				relTarget, err := filepath.Rel(baseDir, targetConfPath)
				if err != nil {
					relTarget = targetConfPath
				}
				fmt.Printf("Created default config: %s\n", filepath.ToSlash(relTarget))
			}
			// Remove the .conf.dist file once .conf is ensured
			_ = os.Remove(path)
		}
		return nil
	})
}

func main() {
	if runtime.GOOS != "windows" {
		fmt.Fprintf(os.Stderr, "Error: mod-playerbots startup tool is only supported on Windows (detected OS: %s)\n", runtime.GOOS)
		os.Exit(1)
	}

	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error parsing arguments: %v\n", err)
		os.Exit(1)
	}

	binaries, err := findMySQLBinaries(opts.mysqlDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	baseDir := findBaseDir()
	authserverExe := findExecutable(baseDir, "authserver")
	worldserverExe := findExecutable(baseDir, "worldserver")

	if !opts.initOnly {
		if authserverExe == "" {
			fmt.Fprintf(os.Stderr, "Error: authserver executable not found in %s or PATH\n", baseDir)
			os.Exit(1)
		}
		if worldserverExe == "" {
			fmt.Fprintf(os.Stderr, "Error: worldserver executable not found in %s or PATH\n", baseDir)
			os.Exit(1)
		}
	}

	// Ensure config files (e.g. worldserver.conf, authserver.conf, modules/playerbots.conf, mysql/my.cnf) exist
	if err := ensureConfigFiles(baseDir, binaries.mysql, opts.mysqlDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure config files: %v\n", err)
	}

	cnfFile := findMySQLConfigFile(opts.mysqlCnf, opts.mysqlDir, baseDir)

	// 1. Initialize data directory if needed
	if !isDataDirInitialized(opts.dataDir) {
		if err := initializeMySQL(binaries, opts.dataDir, cnfFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing MySQL: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("MySQL data directory already initialized at %s\n", opts.dataDir)
	}

	// 2. Start MySQL Server
	fmt.Println("\n=== [2/4] Starting MySQL server ===")
	mysqlCmd, err := startMySQLServer(binaries, opts.dataDir, opts.port, cnfFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting MySQL: %v\n", err)
		os.Exit(1)
	}

	// 3. Wait for MySQL to become ready
	timeoutDuration := time.Duration(opts.timeout) * time.Second
	if err := waitForMySQLReady(binaries, opts.port, timeoutDuration); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		_ = shutdownMySQLAndWait(binaries, opts.port, mysqlCmd)
		os.Exit(1)
	}

	// 4. Apply SQL script
	if !opts.skipSQL {
		if err := runEmbeddedSQL(binaries, opts.port, createMySQLSQL); err != nil {
			fmt.Fprintf(os.Stderr, "Error applying SQL: %v\n", err)
			_ = shutdownMySQLAndWait(binaries, opts.port, mysqlCmd)
			os.Exit(1)
		}
	}

	// 5. If init-only, shutdown and exit
	if opts.initOnly {
		fmt.Println("\nInit-only mode complete. Shutting down MySQL...")
		_ = shutdownMySQLAndWait(binaries, opts.port, mysqlCmd)
		os.Exit(0)
	}

	// 6. Start authserver and worldserver (staggered)
	fmt.Println("\n=== [4/4] Starting authserver and worldserver ===")

	authCmd, err := startServerProcess("authserver", authserverExe, baseDir, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting authserver: %v\n", err)
		_ = shutdownMySQLAndWait(binaries, opts.port, mysqlCmd)
		os.Exit(1)
	}

	authTimeout := time.Duration(opts.timeout*2) * time.Second
	if authTimeout < 60*time.Second {
		authTimeout = 60 * time.Second
	}

	if err := waitForAuthServerReady(opts.authPort, authTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		stopProcess("authserver", authCmd)
		_ = shutdownMySQLAndWait(binaries, opts.port, mysqlCmd)
		os.Exit(1)
	}

	worldCmd, err := startServerProcess("worldserver", worldserverExe, baseDir, os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting worldserver: %v\n", err)
		stopProcess("authserver", authCmd)
		_ = shutdownMySQLAndWait(binaries, opts.port, mysqlCmd)
		os.Exit(1)
	}

	// 7. Setup supervisors with auto-restart capability
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	mysqlSupervisor := newProcessSupervisor("mysqld", func() (*exec.Cmd, error) {
		cmd, err := startMySQLServer(binaries, opts.dataDir, opts.port, cnfFile)
		if err != nil {
			return nil, err
		}
		if err := waitForMySQLReady(binaries, opts.port, 30*time.Second); err != nil {
			_ = cmd.Process.Kill()
			return nil, err
		}
		return cmd, nil
	}, 2*time.Second)

	authSupervisor := newProcessSupervisor("authserver", func() (*exec.Cmd, error) {
		return startServerProcess("authserver", authserverExe, baseDir, nil)
	}, 2*time.Second)

	worldSupervisor := newProcessSupervisor("worldserver", func() (*exec.Cmd, error) {
		return startServerProcess("worldserver", worldserverExe, baseDir, os.Stdin)
	}, 2*time.Second)

	wg.Add(3)
	go mysqlSupervisor.Run(ctx, mysqlCmd, &wg)
	go authSupervisor.Run(ctx, authCmd, &wg)
	go worldSupervisor.Run(ctx, worldCmd, &wg)

	fmt.Println("\n========================================================")
	fmt.Println(" All servers running. You can type commands into console.")
	fmt.Println(" Worldserver and authserver will auto-restart if exited.")
	fmt.Println(" Press Ctrl+C to shut down all servers.")
	fmt.Println("========================================================")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	fmt.Printf("\nReceived signal (%v). Shutting down authserver and worldserver first...\n", sig)
	cancel()

	// 1. Mark supervisors stopped and wait for authserver and worldserver to stop completely
	authSupervisor.MarkStopped()
	worldSupervisor.MarkStopped()

	var gameWg sync.WaitGroup
	gameWg.Add(2)

	go func() {
		defer gameWg.Done()
		authSupervisor.StopAndWait(10 * time.Second)
	}()

	go func() {
		defer gameWg.Done()
		worldSupervisor.StopAndWait(10 * time.Second)
	}()

	gameWg.Wait()
	fmt.Println("Authserver and worldserver stopped successfully.")

	// 2. Last, shut down mysqld after authserver and worldserver are down
	fmt.Println("Shutting down MySQL server...")
	mysqlSupervisor.MarkStopped()
	_ = shutdownMySQL(binaries, opts.port)

	select {
	case <-mysqlSupervisor.doneChan:
		fmt.Println("MySQL server stopped cleanly.")
	case <-time.After(10 * time.Second):
		fmt.Println("MySQL did not stop within 10s, terminating process...")
		mysqlSupervisor.Kill()
		<-mysqlSupervisor.doneChan
	}

	fmt.Println("All servers stopped cleanly.")
}
