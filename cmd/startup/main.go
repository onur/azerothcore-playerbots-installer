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
	"syscall"
	"time"
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
	port     int
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
	fs.IntVar(&opts.port, "port", 3306, "MySQL server port.")
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
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".exe", ".bat", ".cmd", ""}
	}

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

	// Check for core markers of an initialized MySQL data directory
	markers := []string{"mysql", "ibdata1", "auto.cnf"}
	for _, marker := range markers {
		if _, err := os.Stat(filepath.Join(dataDir, marker)); err == nil {
			return true
		}
	}

	// Also check if directory is non-empty
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) == 0 {
		return false
	}

	return false
}

func initializeMySQL(binaries *mysqlBinaries, dataDir string) error {
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute data dir: %w", err)
	}

	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory %s: %w", absDataDir, err)
	}

	fmt.Printf("=== [1/3] Initializing MySQL data directory at %s ===\n", absDataDir)

	args := []string{
		"--initialize-insecure",
		fmt.Sprintf("--datadir=%s", absDataDir),
		"--console",
	}

	cmd := exec.Command(binaries.mysqld, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mysqld --initialize-insecure failed: %w", err)
	}

	fmt.Println("MySQL data directory initialized successfully.")
	return nil
}

func startMySQLServer(binaries *mysqlBinaries, dataDir string, port int) (*exec.Cmd, error) {
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute data dir: %w", err)
	}

	args := []string{
		fmt.Sprintf("--datadir=%s", absDataDir),
		fmt.Sprintf("--port=%d", port),
		"--console",
	}

	fmt.Printf("=== [2/3] Starting MySQL server (Port: %d) ===\n", port)
	cmd := exec.Command(binaries.mysqld, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start mysqld: %w", err)
	}

	return cmd, nil
}

func waitForMySQLReady(binaries *mysqlBinaries, port int, timeout time.Duration, serverExited <-chan error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	fmt.Printf("Waiting for MySQL server to accept connections on port %d...\n", port)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for MySQL server after %v", timeout)
		case err := <-serverExited:
			if err != nil {
				return fmt.Errorf("MySQL server exited unexpectedly: %w", err)
			}
			return errors.New("MySQL server exited unexpectedly")
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

func runEmbeddedSQL(binaries *mysqlBinaries, port int, sqlContent string) error {
	if strings.TrimSpace(sqlContent) == "" {
		return errors.New("embedded SQL content is empty")
	}

	fmt.Println("\n=== [3/3] Applying embedded create_mysql.sql ===")

	args := []string{
		"-u", "root",
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

	fmt.Println("AzerothCore databases and user 'acore' created successfully.")
	return nil
}

func shutdownMySQL(binaries *mysqlBinaries, port int, cmd *exec.Cmd) error {
	fmt.Println("\nShutting down MySQL server...")

	args := []string{
		"-u", "root",
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
		done <- cmd.Wait()
	}()

	select {
	case <-time.After(10 * time.Second):
		if cmd.Process != nil {
			fmt.Println("MySQL did not stop within 10s, terminating process...")
			_ = cmd.Process.Kill()
		}
	case <-done:
		fmt.Println("MySQL server stopped cleanly.")
	}

	return nil
}

func main() {
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

	// 1. Initialize data directory if needed
	if !isDataDirInitialized(opts.dataDir) {
		if err := initializeMySQL(binaries, opts.dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing MySQL: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("MySQL data directory already initialized at %s\n", opts.dataDir)
	}

	// 2. Start MySQL Server
	mysqlCmd, err := startMySQLServer(binaries, opts.dataDir, opts.port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting MySQL: %v\n", err)
		os.Exit(1)
	}

	// Setup channel for process exit
	serverExited := make(chan error, 1)
	go func() {
		serverExited <- mysqlCmd.Wait()
	}()

	// 3. Wait for MySQL to become ready
	timeoutDuration := time.Duration(opts.timeout) * time.Second
	if err := waitForMySQLReady(binaries, opts.port, timeoutDuration, serverExited); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		_ = shutdownMySQL(binaries, opts.port, mysqlCmd)
		os.Exit(1)
	}

	// 4. Apply embedded SQL script
	if !opts.skipSQL {
		if err := runEmbeddedSQL(binaries, opts.port, createMySQLSQL); err != nil {
			fmt.Fprintf(os.Stderr, "Error applying SQL: %v\n", err)
			_ = shutdownMySQL(binaries, opts.port, mysqlCmd)
			os.Exit(1)
		}
	}

	// 5. If init-only, shutdown and exit
	if opts.initOnly {
		fmt.Println("\nInit-only mode complete. Shutting down MySQL...")
		_ = shutdownMySQL(binaries, opts.port, mysqlCmd)
		os.Exit(0)
	}

	// 6. Keep running and listen for termination signals
	fmt.Println("\n========================================================")
	fmt.Println(" Server startup initialized successfully.")
	fmt.Println(" MySQL running in console mode. Press Ctrl+C to stop.")
	fmt.Println("========================================================")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		fmt.Printf("\nReceived signal (%v). Initiating shutdown...\n", sig)
		_ = shutdownMySQL(binaries, opts.port, mysqlCmd)
	case err := <-serverExited:
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nMySQL server exited unexpectedly: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("\nMySQL server process exited.")
	}
}
