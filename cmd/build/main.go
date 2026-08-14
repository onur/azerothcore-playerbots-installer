package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func runCommand(name string, args []string, cwd string) {
	cmd := exec.Command(name, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			fullCmd := append([]string{name}, args...)
			fmt.Fprintf(os.Stderr, "\nCommand failed with exit code %d: %s\n", exitErr.ExitCode(), strings.Join(fullCmd, " "))
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "\nExecutable not found or execution failed: %v\n", err)
		os.Exit(1)
	}
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return nil
}

func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

type configOptions struct {
	config     string
	jobs       int
	mysqlDir   string
	boostDir   string
	opensslDir string
}

func parseArgs() configOptions {
	var opts configOptions
	numCPU := runtime.NumCPU()

	defaultMysql := os.Getenv("MYSQL_ROOT")
	defaultBoost := os.Getenv("BOOST_ROOT")
	defaultOpenSSL := os.Getenv("OPENSSL_ROOT_DIR")

	flag.StringVar(&opts.config, "config", "RelWithDebInfo", "Build configuration (e.g., RelWithDebInfo, Release, Debug).")
	flag.StringVar(&opts.config, "c", "RelWithDebInfo", "Build configuration (shorthand).")

	flag.IntVar(&opts.jobs, "jobs", numCPU, fmt.Sprintf("Number of parallel build jobs. Default: %d", numCPU))
	flag.IntVar(&opts.jobs, "j", numCPU, fmt.Sprintf("Number of parallel build jobs (shorthand). Default: %d", numCPU))

	flag.StringVar(&opts.mysqlDir, "mysql-dir", defaultMysql, "Path to MySQL root directory. Default: $env:MYSQL_ROOT")
	flag.StringVar(&opts.boostDir, "boost-dir", defaultBoost, "Path to Boost root directory. Default: $env:BOOST_ROOT")
	flag.StringVar(&opts.opensslDir, "openssl-dir", defaultOpenSSL, "Path to OpenSSL root directory. Default: $env:OPENSSL_ROOT_DIR")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage of mod-playerbots build tool:\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Build and install AzerothCore with mod-playerbots.\n\nOptions:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	return opts
}

func copyWindowsDLLs(mysqlDir, opensslDir, installDir string) error {
	if mysqlDir == "" {
		return fmt.Errorf("MySQL directory is required on Windows to copy runtime DLLs")
	}

	if opensslDir == "" {
		return fmt.Errorf("OpenSSL directory is required on Windows to copy runtime DLLs")
	}

	mysqlPath, err := filepath.Abs(mysqlDir)
	if err != nil {
		return fmt.Errorf("failed to resolve MySQL path %s: %w", mysqlDir, err)
	}

	opensslPath, err := filepath.Abs(opensslDir)
	if err != nil {
		return fmt.Errorf("failed to resolve OpenSSL path %s: %w", opensslDir, err)
	}

	dllsToCopy := []string{
		filepath.Join(mysqlPath, "lib", "libmysql.dll"),
		filepath.Join(opensslPath, "bin", "libcrypto-3-x64.dll"),
		filepath.Join(opensslPath, "bin", "libssl-3-x64.dll"),
		filepath.Join(opensslPath, "lib", "ossl-modules", "legacy.dll"),
	}

	for _, dll := range dllsToCopy {
		if _, err := os.Stat(dll); err != nil {
			return fmt.Errorf("required DLL not found: %s", dll)
		}

		dest := filepath.Join(installDir, filepath.Base(dll))
		fmt.Printf("Copying %s to %s\n", filepath.Base(dll), installDir)
		if err := copyFile(dll, dest); err != nil {
			return fmt.Errorf("failed to copy %s to %s: %w", dll, dest, err)
		}
	}

	return nil
}

func copyMySQL(mysqlDir, installDir string) error {
	if mysqlDir == "" {
		return fmt.Errorf("MySQL directory is required on Windows to copy MySQL")
	}

	mysqlPath, err := filepath.Abs(mysqlDir)
	if err != nil {
		return fmt.Errorf("failed to resolve MySQL path %s: %w", mysqlDir, err)
	}

	info, err := os.Stat(mysqlPath)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("MySQL directory does not exist or is not a directory: %s", mysqlPath)
	}

	destDir := filepath.Join(installDir, "mysql")
	fmt.Printf("Copying MySQL from %s to %s\n", mysqlPath, destDir)
	if err := copyDir(mysqlPath, destDir); err != nil {
		return fmt.Errorf("failed to copy MySQL directory: %w", err)
	}

	return nil
}

func removeDistExtensions(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".dist") {
			target := strings.TrimSuffix(path, ".dist")
			fmt.Printf("Renaming %s to %s\n", filepath.Base(path), filepath.Base(target))
			if _, err := os.Stat(target); err == nil {
				if err := os.Remove(target); err != nil {
					return fmt.Errorf("failed to remove existing file %s: %w", target, err)
				}
			}
			if err := os.Rename(path, target); err != nil {
				return fmt.Errorf("failed to rename %s to %s: %w", path, target, err)
			}
		}
		return nil
	})
}

func buildStartup(installDir string) error {
	outputPath := filepath.Join(installDir, "startup.exe")
	fmt.Printf("Building startup executable: %s\n", outputPath)

	cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/startup")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build startup binary: %w", err)
	}

	return nil
}

func main() {
	opts := parseArgs()

	sourceDir := "azerothcore-wotlk"
	buildDir := "build"
	installDir := "dist"

	// === [1/3] Configuring CMake ===
	fmt.Println("\n=== [1/3] Configuring CMake ===")
	cmakeConfigureArgs := []string{
		sourceDir,
		"-B", buildDir,
		"-DCMAKE_POLICY_DEFAULT_CMP0175=OLD",
		"-DCMAKE_POLICY_DEFAULT_CMP0153=OLD",
		fmt.Sprintf("-DCMAKE_INSTALL_PREFIX=%s", installDir),
		"-DWITH_WARNINGS=1",
		"-DTOOLS_BUILD=all",
		"-DSCRIPTS=static",
		"-DMODULES=static",
	}

	// Configure MySQL path
	if opts.mysqlDir != "" {
		if mysqlPath, err := filepath.Abs(opts.mysqlDir); err == nil {
			if info, err := os.Stat(mysqlPath); err == nil && info.IsDir() {
				mysqlClean := filepath.ToSlash(mysqlPath)
				fmt.Printf("Using MySQL directory: %s\n", mysqlClean)
				cmakeConfigureArgs = append(cmakeConfigureArgs,
					fmt.Sprintf("-DMYSQL_ROOT_DIR=%s", mysqlClean),
					fmt.Sprintf("-DMYSQL_INCLUDE_DIR=%s/include", mysqlClean),
					fmt.Sprintf("-DMYSQL_LIBRARY=%s/lib/libmysql.lib", mysqlClean),
				)
			}
		}
	}

	// Configure Boost path if specified
	if opts.boostDir != "" {
		if boostPath, err := filepath.Abs(opts.boostDir); err == nil {
			if info, err := os.Stat(boostPath); err == nil && info.IsDir() {
				boostClean := filepath.ToSlash(boostPath)
				fmt.Printf("Using Boost directory: %s\n", boostClean)
				cmakeConfigureArgs = append(cmakeConfigureArgs,
					fmt.Sprintf("-DBOOST_ROOT=%s", boostClean),
					fmt.Sprintf("-DBoost_ROOT=%s", boostClean),
				)
			}
		}
	}

	// Configure OpenSSL path if specified
	if opts.opensslDir != "" {
		if opensslPath, err := filepath.Abs(opts.opensslDir); err == nil {
			if info, err := os.Stat(opensslPath); err == nil && info.IsDir() {
				sslClean := filepath.ToSlash(opensslPath)
				fmt.Printf("Using OpenSSL directory: %s\n", sslClean)
				cmakeConfigureArgs = append(cmakeConfigureArgs,
					fmt.Sprintf("-DOPENSSL_ROOT_DIR=%s", sslClean),
				)
			}
		}
	}

	runCommand("cmake", cmakeConfigureArgs, "")

	// === [2/3] Building Project ===
	fmt.Printf("\n=== [2/3] Building Project (Config: %s, Jobs: %d) ===\n", opts.config, opts.jobs)
	cmakeBuildArgs := []string{
		"--build", buildDir,
		"--config", opts.config,
		"-j", strconv.Itoa(opts.jobs),
	}
	runCommand("cmake", cmakeBuildArgs, "")

	// === [3/3] Installing Binaries ===
	fmt.Printf("\n=== [3/3] Installing Binaries to %s ===\n", installDir)
	cmakeInstallArgs := []string{
		"--install", buildDir,
		"--config", opts.config,
	}
	runCommand("cmake", cmakeInstallArgs, "")

	// Windows-specific packaging: DLLs, MySQL distribution, config renaming, and startup tool
	if runtime.GOOS == "windows" {
		if err := copyWindowsDLLs(opts.mysqlDir, opts.opensslDir, installDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if err := copyMySQL(opts.mysqlDir, installDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		configsDir := filepath.Join(installDir, "configs")
		if err := removeDistExtensions(configsDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if err := buildStartup(installDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("\nBuild and installation completed successfully!")
}
