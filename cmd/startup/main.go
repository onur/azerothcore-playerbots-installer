package main

import (
	"archive/zip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
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

const defaultClientDataURL = "https://github.com/wowgaming/client-data/releases/download/v20.0/Data.zip"

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
	mysqlDir         string
	dataDir          string
	mysqlCnf         string
	port             int
	authPort         int
	timeout          int
	initOnly         bool
	skipSQL          bool
	skipDataCheck    bool
	dataURL          string
	downloadDataOnly bool
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
			ps.mu.Lock()
			stopped := ps.stopped
			ps.mu.Unlock()
			if stopped || ctx.Err() != nil {
				return
			}

			var err error
			cmd, err = ps.startFunc()
			if err != nil {
				ps.mu.Lock()
				stopped = ps.stopped
				ps.mu.Unlock()
				if stopped || ctx.Err() != nil {
					return
				}

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
	fs.StringVar(&opts.dataDir, "data-dir", "", "Path to MySQL data directory. Default: <work-dir>/mysql/data")
	fs.StringVar(&opts.mysqlCnf, "mysql-cnf", "", "Path to MySQL configuration file (my.cnf or my.ini). Default: auto-detected in <work-dir>/mysql")
	fs.IntVar(&opts.port, "port", 3306, "MySQL server port.")
	fs.IntVar(&opts.authPort, "auth-port", 3724, "Authserver realm port.")
	fs.IntVar(&opts.timeout, "timeout", 30, "Timeout in seconds to wait for MySQL to become ready.")
	fs.BoolVar(&opts.initOnly, "init-only", false, "Initialize MySQL data dir, start server, apply SQL script, and exit.")
	fs.BoolVar(&opts.skipSQL, "skip-sql", false, "Skip executing the embedded create_mysql.sql script.")
	fs.BoolVar(&opts.skipDataCheck, "skip-data-check", false, "Skip checking and downloading client data (maps, vmaps, mmaps, dbc).")
	fs.StringVar(&opts.dataURL, "data-url", defaultClientDataURL, "Custom URL to download client data Data.zip from.")
	fs.BoolVar(&opts.downloadDataOnly, "download-data-only", false, "Download and extract client data, then exit.")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of mod-playerbots startup tool:\n\n")
		fmt.Fprintf(fs.Output(), "Start MySQL server in console mode and initialize AzerothCore database.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return opts, err
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

func isClientDataPresent(dataDir string) bool {
	if !dirExists(dataDir) {
		return false
	}

	requiredDirs := []string{"dbc", "maps", "vmaps", "mmaps"}
	for _, req := range requiredDirs {
		subDir := filepath.Join(dataDir, req)
		if !dirExists(subDir) {
			return false
		}
		entries, err := os.ReadDir(subDir)
		if err != nil || len(entries) == 0 {
			return false
		}
	}
	return true
}

func downloadFileWithProgress(ctx context.Context, url string, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("User-Agent", "AzerothCore-Playerbots-Launcher/1.0")

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	tmpPath := destPath + ".download"
	_ = os.MkdirAll(filepath.Dir(tmpPath), 0755)

	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create temporary file %s: %w", tmpPath, err)
	}

	cleanedUp := false
	cleanup := func() {
		if !cleanedUp {
			cleanedUp = true
			out.Close()
			if fileExists(tmpPath) {
				_ = os.Remove(tmpPath)
			}
		}
	}
	defer cleanup()

	totalBytes := resp.ContentLength
	var downloadedBytes int64
	buf := make([]byte, 64*1024)
	startTime := time.Now()
	lastPrintTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := out.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("failed writing to %s: %w", tmpPath, writeErr)
			}
			downloadedBytes += int64(n)

			now := time.Now()
			if now.Sub(lastPrintTime) >= 200*time.Millisecond || readErr != nil {
				lastPrintTime = now
				elapsedSec := now.Sub(startTime).Seconds()
				speedMBps := 0.0
				if elapsedSec > 0 {
					speedMBps = (float64(downloadedBytes) / (1024 * 1024)) / elapsedSec
				}

				if totalBytes > 0 {
					percent := float64(downloadedBytes) / float64(totalBytes) * 100.0
					if percent > 100.0 {
						percent = 100.0
					}
					barWidth := 25
					filled := int(percent / 100.0 * float64(barWidth))
					if filled > barWidth {
						filled = barWidth
					}
					bar := strings.Repeat("=", filled)
					if filled < barWidth {
						bar += ">" + strings.Repeat(" ", barWidth-filled-1)
					}
					etaSec := 0
					if speedMBps > 0 {
						remainingMB := float64(totalBytes-downloadedBytes) / (1024 * 1024)
						etaSec = int(remainingMB / speedMBps)
					}
					fmt.Printf("\r  [%s] %5.1f%% (%6.1f / %6.1f MB) %5.1f MB/s (ETA: %ds)  ",
						bar, percent,
						float64(downloadedBytes)/(1024*1024),
						float64(totalBytes)/(1024*1024),
						speedMBps, etaSec)
				} else {
					fmt.Printf("\r  %6.1f MB downloaded (%5.1f MB/s)  ",
						float64(downloadedBytes)/(1024*1024), speedMBps)
				}
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return fmt.Errorf("error reading response stream: %w", readErr)
		}
	}

	fmt.Println()
	out.Close()
	cleanedUp = true

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to finalize downloaded file: %w", err)
	}

	return nil
}

func extractZip(ctx context.Context, zipPath string, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip archive %s: %w", zipPath, err)
	}
	defer r.Close()

	cleanDestDir := filepath.Clean(destDir)
	if err := os.MkdirAll(cleanDestDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", cleanDestDir, err)
	}

	// Detect if all files share a common root prefix like "Data/" or "data/"
	hasCommonRoot := false
	var commonRoot string
	if len(r.File) > 0 {
		first := filepath.ToSlash(r.File[0].Name)
		if idx := strings.Index(first, "/"); idx != -1 {
			rootCandidate := strings.ToLower(first[:idx])
			if rootCandidate == "data" {
				allMatch := true
				for _, f := range r.File {
					normalized := filepath.ToSlash(f.Name)
					if !strings.HasPrefix(strings.ToLower(normalized), "data/") && strings.ToLower(normalized) != "data" {
						allMatch = false
						break
					}
				}
				if allMatch {
					hasCommonRoot = true
					commonRoot = first[:idx+1]
				}
			}
		}
	}

	totalFiles := len(r.File)
	fmt.Printf("Extracting %d files into %s...\n", totalFiles, cleanDestDir)

	for i, f := range r.File {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		relName := f.Name
		if hasCommonRoot && strings.HasPrefix(relName, commonRoot) {
			relName = strings.TrimPrefix(relName, commonRoot)
		}
		if relName == "" || relName == "." {
			continue
		}

		targetPath := filepath.Join(cleanDestDir, relName)
		cleanTarget := filepath.Clean(targetPath)
		if !strings.HasPrefix(cleanTarget, cleanDestDir+string(filepath.Separator)) && cleanTarget != cleanDestDir {
			return fmt.Errorf("illegal file path in zip archive: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return fmt.Errorf("failed to create destination file %s: %w", targetPath, err)
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return fmt.Errorf("failed to read zip entry %s: %w", f.Name, err)
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return fmt.Errorf("failed writing to %s: %w", targetPath, err)
		}

		if (i+1)%500 == 0 || i+1 == totalFiles {
			fmt.Printf("\r  Extracting client data: %d / %d files (%d%%)  ", i+1, totalFiles, (i+1)*100/totalFiles)
		}
	}

	fmt.Println("\nClient data extracted successfully.")
	return nil
}

func ensureClientData(ctx context.Context, workDir, baseDir, dataURL string, skipCheck bool) error {
	if skipCheck {
		return nil
	}

	workDataDir := filepath.Join(workDir, "data")
	baseDataDir := filepath.Join(baseDir, "data")

	// 1. Check if client data is already present in baseDir/data or workDir/data
	if dirExists(baseDataDir) && isClientDataPresent(baseDataDir) {
		fmt.Printf("Client data verified at %s.\n", baseDataDir)
		return nil
	}
	if dirExists(workDataDir) && isClientDataPresent(workDataDir) {
		fmt.Printf("Client data verified at %s.\n", workDataDir)
		return nil
	}

	fmt.Println("\n=== Client Data Verification ===")
	fmt.Printf("Required client data (dbc, maps, vmaps, mmaps) not found in %s.\n", workDataDir)
	if dataURL == "" {
		dataURL = defaultClientDataURL
	}

	if err := os.MkdirAll(workDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory %s: %w", workDataDir, err)
	}

	zipPath := filepath.Join(workDataDir, "Data.zip")
	fmt.Printf("Downloading client data from %s...\n", dataURL)

	if err := downloadFileWithProgress(ctx, dataURL, zipPath); err != nil {
		return fmt.Errorf("failed to download client data: %w", err)
	}
	defer func() {
		if fileExists(zipPath) {
			_ = os.Remove(zipPath)
		}
	}()

	if err := extractZip(ctx, zipPath, workDataDir); err != nil {
		return fmt.Errorf("failed to extract client data: %w", err)
	}

	if !isClientDataPresent(workDataDir) {
		return fmt.Errorf("client data extraction completed, but required folders (dbc, maps, vmaps, mmaps) are missing in %s", workDataDir)
	}

	return nil
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
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

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
		"--bind-address=127.0.0.1",
		"--mysqlx=0",
		"--console",
	)

	fmt.Printf("=== Starting MySQL server (Port: %d) ===\n", port)
	cmd := exec.Command(binaries.mysqld, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start mysqld: %w", err)
	}

	return cmd, nil
}

func isProcessAlive(pid int) (bool, uint32) {
	if pid <= 0 {
		return false, 0
	}
	if runtime.GOOS != "windows" {
		p, err := os.FindProcess(pid)
		if err != nil {
			return false, 0
		}
		err = p.Signal(syscall.Signal(0))
		return err == nil, 0
	}

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel32.NewProc("OpenProcess")
	getExitCodeProcess := kernel32.NewProc("GetExitCodeProcess")
	waitForSingleObject := kernel32.NewProc("WaitForSingleObject")
	closeHandle := kernel32.NewProc("CloseHandle")

	const SYNCHRONIZE = 0x00100000
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	const WAIT_TIMEOUT = 258 // 0x102
	const STILL_ACTIVE = 259

	hProcess, _, _ := openProcess.Call(SYNCHRONIZE|PROCESS_QUERY_LIMITED_INFORMATION, 0, uintptr(pid))
	if hProcess == 0 {
		return false, 0
	}
	defer closeHandle.Call(hProcess)

	waitRet, _, _ := waitForSingleObject.Call(hProcess, 0)
	if waitRet == WAIT_TIMEOUT {
		return true, STILL_ACTIVE
	}

	var exitCode uint32
	ret, _, _ := getExitCodeProcess.Call(hProcess, uintptr(unsafe.Pointer(&exitCode)))
	if ret == 0 {
		return false, 0
	}
	return false, exitCode
}

func waitForMySQLReady(ctx context.Context, cmd *exec.Cmd, binaries *mysqlBinaries, port int, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	fmt.Printf("Waiting for MySQL server to accept connections on port %d...\n", port)

	for {
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.Canceled) {
				return errors.New("interrupted waiting for MySQL server")
			}
			return fmt.Errorf("timed out waiting for MySQL server after %v", timeout)
		case <-ticker.C:
			if cmd != nil && cmd.Process != nil {
				alive, exitCode := isProcessAlive(cmd.Process.Pid)
				if !alive {
					return fmt.Errorf("MySQL server process exited prematurely (PID: %d, exit code: %d)", cmd.Process.Pid, exitCode)
				}
			}

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

func waitForAuthServerReady(ctx context.Context, cmd *exec.Cmd, port int, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	fmt.Printf("Waiting for authserver to initialize and listen on port %d...\n", port)

	for {
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.Canceled) {
				return errors.New("interrupted waiting for authserver")
			}
			return fmt.Errorf("timed out waiting for authserver on port %d after %v", port, timeout)
		case <-ticker.C:
			if cmd != nil && cmd.Process != nil {
				alive, exitCode := isProcessAlive(cmd.Process.Pid)
				if !alive {
					return fmt.Errorf("authserver process exited prematurely (PID: %d, exit code: %d)", cmd.Process.Pid, exitCode)
				}
			}

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

func sanitizeRealmlistFlags(binaries *mysqlBinaries, port int) {
	// Reset any stale realm flags (e.g. flag 1 or 3) left behind if worldserver was interrupted/killed mid-boot.
	query := "UPDATE `acore_auth`.`realmlist` SET `flag` = 0 WHERE `flag` IN (1, 3) OR (`flag` & 1) != 0;"
	args := []string{
		"-u", "root",
		"-h", "127.0.0.1",
		"-P", fmt.Sprintf("%d", port),
		"--protocol=tcp",
		"-e", query,
	}

	cmd := exec.Command(binaries.mysql, args...)
	_ = cmd.Run()
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
	if err := shutdownCmd.Run(); err != nil {
		return fmt.Errorf("mysqladmin shutdown failed: %w", err)
	}

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
			<-done
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

	redoLogCapacity := "1G"
	if totalRAMGB >= 16 {
		redoLogCapacity = "2G"
	} else if totalRAMGB > 0 && totalRAMGB < 8 {
		redoLogCapacity = "512M"
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
# Basic Network and Connection Settings (Loopback only to avoid firewall alerts)
bind-address = 127.0.0.1
mysqlx = 0
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
innodb_redo_log_capacity = %s
innodb_log_buffer_size = 64M
innodb_io_capacity = 1000
innodb_io_capacity_max = 3000
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
`, ramComment, poolSize, poolInstances, redoLogCapacity)
}

var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentPackageFullName = kernel32.NewProc("GetCurrentPackageFullName")
)

const appModelErrorNoPackage = 15700

// isPackagedApp returns true if the process is running inside an MSIX/AppX package.
func isPackagedApp() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if err := procGetCurrentPackageFullName.Find(); err != nil {
		return false
	}
	var length uint32
	r1, _, _ := procGetCurrentPackageFullName.Call(
		uintptr(unsafe.Pointer(&length)),
		uintptr(0),
	)
	return r1 != uintptr(appModelErrorNoPackage)
}

// isDirWritable checks whether a directory exists and files can be created in it.
func isDirWritable(dir string) bool {
	if !dirExists(dir) {
		return false
	}
	testFile := filepath.Join(dir, fmt.Sprintf(".write_test_%d", time.Now().UnixNano()))
	if err := os.WriteFile(testFile, []byte("ok"), 0644); err != nil {
		return false
	}
	_ = os.Remove(testFile)
	return true
}

func ensureWorkDirStructure(dir string) {
	_ = os.MkdirAll(dir, 0755)
	_ = os.MkdirAll(filepath.Join(dir, "configs"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "logs"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "data"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "mysql"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "mysql", "data"), 0755)
}

func getWorkDir(baseDir string) string {
	if envDir := os.Getenv("PLAYERBOTS_WORKDIR"); envDir != "" {
		ensureWorkDirStructure(envDir)
		return envDir
	}

	if !isPackagedApp() && isDirWritable(baseDir) {
		ensureWorkDirStructure(baseDir)
		return baseDir
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
			localAppData = filepath.Join(userProfile, "AppData", "Local")
		} else if home := os.Getenv("HOME"); home != "" {
			localAppData = filepath.Join(home, ".local", "share")
		} else {
			localAppData = "."
		}
	}

	appDir := filepath.Join(localAppData, "Playerbots")
	ensureWorkDirStructure(appDir)
	return appDir
}

func findMySQLConfigFile(customPath, mysqlDir, baseDir string, workDir ...string) string {
	if customPath != "" && fileExists(customPath) {
		return customPath
	}

	var candidates []string

	for _, w := range workDir {
		if w != "" {
			candidates = append(candidates,
				filepath.Join(w, "mysql", "my.cnf"),
				filepath.Join(w, "mysql", "my.ini"),
				filepath.Join(w, "configs", "my.cnf"),
				filepath.Join(w, "configs", "my.ini"),
				filepath.Join(w, "my.cnf"),
				filepath.Join(w, "my.ini"),
			)
		}
	}

	if mysqlDir != "" {
		candidates = append(candidates,
			filepath.Join(mysqlDir, "my.cnf"),
			filepath.Join(mysqlDir, "my.ini"),
		)
	}

	candidates = append(candidates,
		filepath.Join(baseDir, "mysql", "my.cnf"),
		filepath.Join(baseDir, "mysql", "my.ini"),
		filepath.Join(baseDir, "configs", "my.cnf"),
		filepath.Join(baseDir, "configs", "my.ini"),
		filepath.Join(baseDir, "my.cnf"),
		filepath.Join(baseDir, "my.ini"),
	)

	for _, cand := range candidates {
		if cand != "" && fileExists(cand) {
			return cand
		}
	}

	return ""
}

func ensureMySQLConfigFile(baseDir, workDir, mysqlDir string) (string, error) {
	if existing := findMySQLConfigFile("", mysqlDir, baseDir, workDir); existing != "" {
		return existing, nil
	}

	targetDir := filepath.Join(workDir, "mysql")
	targetPath := filepath.Join(targetDir, "my.cnf")

	poolSize, instances, ramGB := calculateMySQLBufferPoolSettings()
	content := generateDefaultMyCnf(poolSize, instances, ramGB)

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory for %s: %w", targetPath, err)
	}

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", targetPath, err)
	}

	relTarget, err := filepath.Rel(workDir, targetPath)
	if err != nil {
		relTarget = targetPath
	}
	fmt.Printf("Created default MySQL config: %s (Buffer pool: %s, Instances: %d)\n", filepath.ToSlash(relTarget), poolSize, instances)

	return targetPath, nil
}

func ensureConfigFiles(baseDir, workDir string, mysqlExePath string, mysqlDir ...string) error {
	resolvedMySQLDir := ""
	if len(mysqlDir) > 0 && mysqlDir[0] != "" {
		resolvedMySQLDir = mysqlDir[0]
	} else if mysqlExePath != "" {
		resolvedMySQLDir = filepath.Dir(filepath.Dir(mysqlExePath))
	} else {
		resolvedMySQLDir = filepath.Join(workDir, "mysql")
	}

	// 1. Ensure MySQL configuration file (my.cnf) exists with fine-tuning settings
	if _, err := ensureMySQLConfigFile(baseDir, workDir, resolvedMySQLDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure MySQL config file: %v\n", err)
	}

	// 2. Ensure server configs and logs directory in workDir
	_ = os.MkdirAll(filepath.Join(workDir, "logs"), 0755)
	configSrcDir := filepath.Join(baseDir, "configs")
	if !dirExists(configSrcDir) {
		return nil
	}
	configDstDir := filepath.Join(workDir, "configs")
	_ = os.MkdirAll(configDstDir, 0755)

	mysqlExeForConf := "mysql/bin/mysql.exe"
	if mysqlExePath != "" {
		if rel, err := filepath.Rel(workDir, mysqlExePath); err == nil && !strings.HasPrefix(rel, "..") {
			mysqlExeForConf = filepath.ToSlash(rel)
		} else {
			mysqlExeForConf = filepath.ToSlash(mysqlExePath)
		}
	}

	srcDirForConf := "src"
	if srcDirAbs := filepath.Join(baseDir, "src"); dirExists(srcDirAbs) {
		srcDirForConf = filepath.ToSlash(srcDirAbs)
	}

	dataDirForConf := "data"
	baseDataDir := filepath.Join(baseDir, "data")
	workDataDir := filepath.Join(workDir, "data")
	if dirExists(baseDataDir) && baseDataDir != workDataDir {
		if entries, err := os.ReadDir(baseDataDir); err == nil && len(entries) > 0 {
			dataDirForConf = filepath.ToSlash(baseDataDir)
		}
	}

	return filepath.Walk(configSrcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		if strings.HasSuffix(info.Name(), ".conf.dist") || strings.HasSuffix(info.Name(), ".conf") {
			relPath, err := filepath.Rel(configSrcDir, path)
			if err != nil {
				relPath = info.Name()
			}
			relTargetName := strings.TrimSuffix(relPath, ".dist")
			targetConfPath := filepath.Join(configDstDir, relTargetName)

			if !fileExists(targetConfPath) {
				data, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("failed to read %s: %w", path, err)
				}

				content := string(data)
				content = strings.Replace(content, `DataDir = "."`, fmt.Sprintf(`DataDir = "%s"`, dataDirForConf), 1)
				content = strings.Replace(content, `LogsDir = ""`, `LogsDir = "logs"`, 1)
				content = strings.Replace(content, `SourceDirectory = ""`, fmt.Sprintf(`SourceDirectory = "%s"`, srcDirForConf), 1)
				content = strings.Replace(content, `MySQLExecutable = ""`, fmt.Sprintf(`MySQLExecutable = "%s"`, mysqlExeForConf), 1)
				content = strings.Replace(content, `BindIP = "0.0.0.0"`, `BindIP = "127.0.0.1"`, 1)

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

				relTarget, err := filepath.Rel(workDir, targetConfPath)
				if err != nil {
					relTarget = targetConfPath
				}
				fmt.Printf("Created default config: %s (BindIP: 127.0.0.1)\n", filepath.ToSlash(relTarget))
			}
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
	workDir := getWorkDir(baseDir)
	if workDir != baseDir {
		fmt.Printf("Running in packaged mode. Working directory: %s\n", workDir)
	} else {
		fmt.Printf("Working directory: %s\n", workDir)
	}

	// Set up early root context and signal handling so Ctrl+C at any time shuts down child processes
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-sigChan:
			fmt.Printf("\nReceived signal (%v)...\n", sig)
			rootCancel()
		case <-rootCtx.Done():
		}
	}()

	// Handle -download-data-only mode
	if opts.downloadDataOnly {
		if err := ensureClientData(rootCtx, workDir, baseDir, opts.dataURL, false); err != nil {
			fmt.Fprintf(os.Stderr, "Error downloading client data: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Client data download and extraction complete.")
		os.Exit(0)
	}

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

	// Default dataDir to <workDir>/mysql/data
	if opts.dataDir == "" {
		opts.dataDir = filepath.Join(workDir, "mysql", "data")
	}

	// Ensure config files (e.g. worldserver.conf, authserver.conf, modules/playerbots.conf, mysql/my.cnf) exist in workDir
	if err := ensureConfigFiles(baseDir, workDir, binaries.mysql, opts.mysqlDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure config files: %v\n", err)
	}

	// Ensure client data files (maps, vmaps, mmaps, dbc) are present
	if !opts.initOnly {
		if err := ensureClientData(rootCtx, workDir, baseDir, opts.dataURL, opts.skipDataCheck); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	cnfFile := findMySQLConfigFile(opts.mysqlCnf, opts.mysqlDir, baseDir, workDir)

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
	if err := waitForMySQLReady(rootCtx, mysqlCmd, binaries, opts.port, timeoutDuration); err != nil {
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

	// Reset any leftover realm status flags (e.g. flag = 1 or 3 from previous interrupted worldserver boots)
	sanitizeRealmlistFlags(binaries, opts.port)

	authCmd, err := startServerProcess("authserver", authserverExe, workDir, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting authserver: %v\n", err)
		_ = shutdownMySQLAndWait(binaries, opts.port, mysqlCmd)
		os.Exit(1)
	}

	authTimeout := time.Duration(opts.timeout*2) * time.Second
	if authTimeout < 60*time.Second {
		authTimeout = 60 * time.Second
	}

	if err := waitForAuthServerReady(rootCtx, authCmd, opts.authPort, authTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		stopProcess("authserver", authCmd)
		_ = shutdownMySQLAndWait(binaries, opts.port, mysqlCmd)
		os.Exit(1)
	}

	worldCmd, err := startServerProcess("worldserver", worldserverExe, workDir, os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting worldserver: %v\n", err)
		stopProcess("authserver", authCmd)
		_ = shutdownMySQLAndWait(binaries, opts.port, mysqlCmd)
		os.Exit(1)
	}

	// 7. Setup supervisors with auto-restart capability
	supervisorCtx, supervisorCancel := context.WithCancel(context.Background())
	defer supervisorCancel()

	var wg sync.WaitGroup

	mysqlSupervisor := newProcessSupervisor("mysqld", func() (*exec.Cmd, error) {
		cmd, err := startMySQLServer(binaries, opts.dataDir, opts.port, cnfFile)
		if err != nil {
			return nil, err
		}
		if err := waitForMySQLReady(supervisorCtx, cmd, binaries, opts.port, 30*time.Second); err != nil {
			_ = cmd.Process.Kill()
			return nil, err
		}
		return cmd, nil
	}, 2*time.Second)

	authSupervisor := newProcessSupervisor("authserver", func() (*exec.Cmd, error) {
		sanitizeRealmlistFlags(binaries, opts.port)
		return startServerProcess("authserver", authserverExe, workDir, nil)
	}, 2*time.Second)

	worldSupervisor := newProcessSupervisor("worldserver", func() (*exec.Cmd, error) {
		return startServerProcess("worldserver", worldserverExe, workDir, os.Stdin)
	}, 2*time.Second)

	wg.Add(3)
	go mysqlSupervisor.Run(supervisorCtx, mysqlCmd, &wg)
	go authSupervisor.Run(supervisorCtx, authCmd, &wg)
	go worldSupervisor.Run(supervisorCtx, worldCmd, &wg)

	fmt.Println("\n========================================================")
	fmt.Println(" All servers running. You can type commands into console.")
	fmt.Println(" Worldserver and authserver will auto-restart if exited.")
	fmt.Println(" Press Ctrl+C to shut down all servers.")
	fmt.Println("========================================================")

	<-rootCtx.Done()
	fmt.Println("\nShutting down authserver and worldserver first...")

	// 1. Mark supervisors stopped and wait for authserver and worldserver to stop completely
	authSupervisor.MarkStopped()
	worldSupervisor.MarkStopped()

	var gameWg sync.WaitGroup
	gameWg.Add(2)

	go func() {
		defer gameWg.Done()
		authSupervisor.StopAndWait(60 * time.Second)
	}()

	go func() {
		defer gameWg.Done()
		worldSupervisor.StopAndWait(60 * time.Second)
	}()

	gameWg.Wait()
	fmt.Println("Authserver and worldserver stopped successfully.")

	// 2. Last, shut down mysqld after authserver and worldserver are down
	fmt.Println("Shutting down MySQL server...")
	mysqlSupervisor.MarkStopped()
	supervisorCancel()

	if err := shutdownMySQL(binaries, opts.port); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	select {
	case <-mysqlSupervisor.doneChan:
		fmt.Println("MySQL server stopped cleanly.")
	case <-time.After(30 * time.Second):
		fmt.Println("MySQL did not stop within 30s, terminating process...")
		mysqlSupervisor.Kill()
		<-mysqlSupervisor.doneChan
	}

	fmt.Println("All servers stopped cleanly.")
}
