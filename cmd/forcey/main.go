package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var version = "dev"

const (
	seeMaskNoCloseProcess  = 0x00000040
	swShownormal           = 1
	infinite               = 0xFFFFFFFF
	fileAttributeDirectory = 0x00000010
	invalidFileAttributes  = 0xFFFFFFFF
)

type options struct {
	yes         bool
	dryRun      bool
	allowSystem bool
	elevated    bool
	confirmed   bool
	help        bool
	showVersion bool
	showLicense bool
	targets     []string
}

type handleLock struct {
	process string
	pid     int
	handle  string
	path    string
}

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     syscall.Handle
}

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procIsUserAnAdmin    = shell32.NewProc("IsUserAnAdmin")
	procShellExecuteExW  = shell32.NewProc("ShellExecuteExW")
	procWaitForSingleObj = kernel32.NewProc("WaitForSingleObject")
	procGetExitCode      = kernel32.NewProc("GetExitCodeProcess")
	procCloseHandle      = kernel32.NewProc("CloseHandle")
	procGetFileAttrs     = kernel32.NewProc("GetFileAttributesW")
)

func main() {
	os.Exit(run())
}

func run() int {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fail(err.Error())
		return 2
	}

	if opts.help || len(os.Args) == 1 {
		printHelp()
		return 0
	}

	if opts.showVersion {
		fmt.Printf("forcey %s\n", version)
		return 0
	}

	if opts.showLicense {
		printLicenseNotice()
		return 0
	}

	targets, err := resolveTargets(opts.targets)
	if err != nil {
		fail(err.Error())
		return 2
	}

	if len(targets) == 0 {
		info("Nothing to delete.")
		return 0
	}

	if err := validateTargets(targets, opts.allowSystem); err != nil {
		fail(err.Error())
		return 4
	}

	fmt.Println()
	info("Targets:")
	for _, target := range targets {
		fmt.Printf("  - %s\n", target)
	}

	if opts.dryRun {
		fmt.Println()
		info("Dry-run force ladder:")
		fmt.Println("  1. Normal deletion")
		fmt.Println("  2. Clear restrictive attributes")
		fmt.Println("  3. Elevate only if still required")
		fmt.Println("  4. Retry as administrator")
		fmt.Println("  5. Reset permissions")
		fmt.Println("  6. Take ownership and grant access")
		fmt.Println("  7. Close locking handles as the final step")
		info("Nothing was changed.")
		return 0
	}

	if !opts.confirmed && !opts.yes {
		warn(fmt.Sprintf("This permanently deletes %d target(s).", len(targets)))
		fmt.Print("Type DELETE to continue: ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if strings.TrimSpace(answer) != "DELETE" {
			info("Cancelled.")
			return 0
		}
	}

	leaveTargetDirectory(targets)

	if !opts.elevated {
		remaining := runLevel(1, "normal deletion", targets, func(items []string) {
			deleteTargets(items)
		})
		if len(remaining) == 0 {
			return success(targets, 1)
		}

		remaining = runLevel(2, "clearing restrictive attributes", remaining, func(items []string) {
			clearAttributes(items)
			deleteTargets(items)
		})
		if len(remaining) == 0 {
			return success(targets, 2)
		}

		if !isAdministrator() {
			printDeleted(targets, remaining)
			return elevate(remaining, opts.allowSystem)
		}

		targets = remaining
	}

	remaining := runLevel(3, "normal deletion with administrator rights", targets, func(items []string) {
		deleteTargets(items)
	})
	if len(remaining) == 0 {
		return success(targets, 3)
	}

	remaining = runLevel(4, "administrator attribute cleanup", remaining, func(items []string) {
		clearAttributes(items)
		deleteTargets(items)
	})
	if len(remaining) == 0 {
		return success(targets, 4)
	}

	remaining = runLevel(5, "resetting permissions", remaining, func(items []string) {
		resetPermissions(items)
		clearAttributes(items)
		deleteTargets(items)
	})
	if len(remaining) == 0 {
		return success(targets, 5)
	}

	remaining = runLevel(6, "taking ownership and granting deletion access", remaining, func(items []string) {
		grantDeletionAccess(items)
		clearAttributes(items)
		deleteTargets(items)
	})
	if len(remaining) == 0 {
		return success(targets, 6)
	}

	stage(7, "closing locking handles")
	handleExe, err := getHandleExecutable()
	if err != nil {
		printResults(targets, remaining)
		fail(err.Error())
		return 5
	}

	locks := findLocks(handleExe, remaining)
	if len(locks) == 0 {
		info("No matching open handles were found.")
	} else {
		warn(fmt.Sprintf("Found %d unique locking handle(s).", len(locks)))
		for _, lock := range locks {
			fmt.Printf("  %s (PID %d), handle %s: %s\n", lock.process, lock.pid, lock.handle, lock.path)
		}
	}

	if hasCriticalLock(locks) && !opts.allowSystem {
		printResults(targets, remaining)
		fail("A critical Windows process holds at least one target. Use --allow-system only if you explicitly accept system instability.")
		return 6
	}

	closeLocks(handleExe, locks)
	time.Sleep(250 * time.Millisecond)
	grantDeletionAccess(remaining)
	clearAttributes(remaining)
	deleteTargets(remaining)
	remaining = existingTargets(remaining)
	printResults(targets, remaining)

	if len(remaining) > 0 {
		fail(fmt.Sprintf("%d target(s) survived every force level.", len(remaining)))
		return 7
	}

	fmt.Println("✨ forcey deleted everything at force level 7. Brutally, but only when needed.")
	return 0
}

func parseOptions(args []string) (options, error) {
	var opts options
	flagsEnabled := true

	for _, arg := range args {
		if flagsEnabled && arg == "--" {
			flagsEnabled = false
			continue
		}

		if flagsEnabled && strings.HasPrefix(arg, "-") {
			switch arg {
			case "-y", "--yes":
				opts.yes = true
			case "-n", "--dry-run":
				opts.dryRun = true
			case "--allow-system":
				opts.allowSystem = true
			case "--elevated":
				opts.elevated = true
			case "--confirmed":
				opts.confirmed = true
			case "-h", "--help":
				opts.help = true
			case "-v", "--version":
				opts.showVersion = true
			case "--license":
				opts.showLicense = true
			default:
				return opts, fmt.Errorf("unknown option: %s", arg)
			}
			continue
		}

		opts.targets = append(opts.targets, arg)
	}

	if !opts.help && !opts.showVersion && !opts.showLicense && len(opts.targets) == 0 {
		return opts, errors.New("at least one target path is required")
	}

	return opts, nil
}

func printHelp() {
	fmt.Printf(`forcey %s

A cute command for progressively deleting stubborn files and directories on Windows.

Usage:
  forcey <path> [<path> ...]
  forcey <path> [<path> ...] --yes
  forcey <path> [<path> ...] --dry-run

Options:
  -y, --yes           Skip the DELETE confirmation
  -n, --dry-run       Show the plan without changing anything
      --allow-system  Permit protected Windows locations and critical handles
  -v, --version       Show the version
      --license       Show the license notice
  -h, --help          Show this help

Examples:
  forcey .\stubborn-folder
  forcey .\folder-one ".\folder two" .\locked-file.exe
  forcey C:\Temp\old-folder --yes

Deletion is permanent and bypasses the Recycle Bin.
Licensed under GNU AGPL v3.0 only.
`, version)
}

func printLicenseNotice() {
	executable, err := os.Executable()
	if err == nil {
		licensePath := filepath.Join(filepath.Dir(executable), "LICENSE")
		if contents, readErr := os.ReadFile(licensePath); readErr == nil {
			fmt.Print(string(contents))
			return
		}
	}
	fmt.Println("forcey is licensed under the GNU Affero General Public License, version 3 only.")
	fmt.Println("This program comes with absolutely no warranty.")
	fmt.Println("The complete license is available in the source repository and release package.")
}

func resolveTargets(rawTargets []string) ([]string, error) {
	seen := map[string]bool{}
	var targets []string

	for _, raw := range rawTargets {
		expanded := expandWindowsEnvironment(raw)
		absolute, err := filepath.Abs(expanded)
		if err != nil {
			return nil, fmt.Errorf("invalid path %q: %w", raw, err)
		}
		clean := filepath.Clean(absolute)
		key := strings.ToLower(clean)
		if seen[key] {
			continue
		}
		seen[key] = true

		exists, err := pathExists(clean)
		if err != nil {
			return nil, fmt.Errorf("cannot inspect %q: %w", clean, err)
		}
		if !exists {
			warn(fmt.Sprintf("Target does not exist, skipping: %s", clean))
			continue
		}
		targets = append(targets, clean)
	}

	return targets, nil
}

func expandWindowsEnvironment(value string) string {
	re := regexp.MustCompile(`%([^%]+)%`)
	value = re.ReplaceAllStringFunc(value, func(match string) string {
		name := strings.Trim(match, "%")
		if replacement, ok := os.LookupEnv(name); ok {
			return replacement
		}
		return match
	})
	return os.ExpandEnv(value)
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	if errors.Is(err, os.ErrPermission) {
		return true, nil
	}
	return false, err
}

func validateTargets(targets []string, allowSystem bool) error {
	home, _ := os.UserHomeDir()
	profilesRoot := filepath.Dir(home)
	executable, _ := os.Executable()
	systemLocations := []string{
		os.Getenv("SystemRoot"),
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramData"),
	}

	for _, target := range targets {
		volumeRoot := filepath.VolumeName(target) + `\`
		for _, protected := range []string{volumeRoot, home, profilesRoot} {
			if protected != "" && strings.EqualFold(filepath.Clean(target), filepath.Clean(protected)) {
				return fmt.Errorf("refusing to delete protected path: %s", target)
			}
		}

		if executable != "" && sameOrInside(executable, target) {
			return errors.New("refusing to delete forcey's own executable while it is running")
		}

		if !allowSystem {
			for _, systemLocation := range systemLocations {
				if systemLocation != "" && sameOrInside(target, systemLocation) {
					return fmt.Errorf("protected system target: %s; use --allow-system only when absolutely certain", target)
				}
			}
		}
	}

	return nil
}

func sameOrInside(candidate, parent string) bool {
	candidate = filepath.Clean(candidate)
	parent = filepath.Clean(parent)
	if strings.EqualFold(candidate, parent) {
		return true
	}
	prefix := parent
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	return strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(prefix))
}

func leaveTargetDirectory(targets []string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	inside := false
	for _, target := range targets {
		if sameOrInside(cwd, target) {
			inside = true
			break
		}
	}
	if !inside {
		return
	}

	safe := filepath.Dir(cwd)
	for {
		blocked := false
		for _, target := range targets {
			if sameOrInside(safe, target) {
				blocked = true
				break
			}
		}
		if !blocked {
			break
		}
		next := filepath.Dir(safe)
		if next == safe {
			safe = filepath.VolumeName(cwd) + `\`
			break
		}
		safe = next
	}

	_ = os.Chdir(safe)
	info(fmt.Sprintf("forcey's helper process temporarily moved to: %s", safe))
}

func runLevel(level int, description string, targets []string, action func([]string)) []string {
	stage(level, description)
	action(targets)
	return existingTargets(targets)
}

func deleteTargets(targets []string) {
	ordered := append([]string(nil), targets...)
	sort.Slice(ordered, func(i, j int) bool {
		return len(ordered[i]) > len(ordered[j])
	})
	for _, target := range ordered {
		_ = os.RemoveAll(target)
	}
}

func existingTargets(targets []string) []string {
	remaining := make([]string, 0, len(targets))
	for _, target := range targets {
		exists, _ := pathExists(target)
		if exists {
			remaining = append(remaining, target)
		}
	}
	return remaining
}

func clearAttributes(targets []string) {
	attrib := filepath.Join(os.Getenv("SystemRoot"), "System32", "attrib.exe")
	for _, target := range targets {
		runQuiet(attrib, "-R", "-S", "-H", target)
		if isDirectory(target) {
			runQuiet(attrib, "-R", "-S", "-H", filepath.Join(target, "*"), "/S", "/D")
		}
	}
}

func resetPermissions(targets []string) {
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	for _, target := range targets {
		args := []string{target, "/reset", "/C", "/Q"}
		if isDirectory(target) {
			args = append(args, "/T")
		}
		runQuiet(icacls, args...)
	}
}

func grantDeletionAccess(targets []string) {
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	const administrators = "*S-1-5-32-544"

	for _, target := range targets {
		ownerArgs := []string{target, "/setowner", administrators, "/C", "/Q"}
		grant := administrators + ":F"
		if isDirectory(target) {
			ownerArgs = append(ownerArgs, "/T")
			grant = administrators + ":(OI)(CI)F"
		}
		runQuiet(icacls, ownerArgs...)

		grantArgs := []string{target, "/grant:r", grant, "/C", "/Q"}
		if isDirectory(target) {
			grantArgs = append(grantArgs, "/T")
		}
		runQuiet(icacls, grantArgs...)
	}
}

func isDirectory(path string) bool {
	pointer, err := syscall.UTF16PtrFromString(path)
	if err == nil {
		attributes, _, _ := procGetFileAttrs.Call(uintptr(unsafe.Pointer(pointer)))
		value := uint32(attributes)
		if value != invalidFileAttributes {
			return value&fileAttributeDirectory != 0
		}
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func runQuiet(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	_ = cmd.Run()
}

func isAdministrator() bool {
	result, _, _ := procIsUserAnAdmin.Call()
	return result != 0
}

func elevate(targets []string, allowSystem bool) int {
	warn(fmt.Sprintf("%d target(s) remain; administrator rights are required.", len(targets)))
	executable, err := os.Executable()
	if err != nil {
		fail(fmt.Sprintf("cannot locate forcey.exe: %v", err))
		return 5
	}

	args := []string{"--elevated", "--confirmed"}
	if allowSystem {
		args = append(args, "--allow-system")
	}
	args = append(args, "--")
	args = append(args, targets...)

	if sudo := windowsSudo(); sudo != "" {
		info("Windows sudo is available and enabled; using sudo.")
		cmd := exec.Command(sudo, append([]string{executable}, args...)...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err == nil {
			return 0
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		warn("Windows sudo could not start forcey; falling back to the standard UAC prompt.")
	}

	info("Using the standard UAC prompt.")
	code, err := shellExecuteRunAs(executable, args)
	if err != nil {
		fail(fmt.Sprintf("elevation failed: %v", err))
		return 5
	}
	return code
}

func windowsSudo() string {
	path, err := exec.LookPath("sudo.exe")
	if err != nil {
		return ""
	}

	policyKey := `HKLM\SOFTWARE\Policies\Microsoft\Windows\Sudo`
	for _, valueName := range []string{"EnableSudo", "Enabled"} {
		if value, ok := queryRegistryDword(policyKey, valueName); ok && value == 0 {
			return ""
		}
	}
	value, ok := queryRegistryDword(`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Sudo`, "Enabled")
	if !ok || value == 0 {
		return ""
	}
	return path
}

func queryRegistryDword(key, name string) (uint64, bool) {
	output, err := exec.Command("reg.exe", "query", key, "/v", name).CombinedOutput()
	if err != nil {
		return 0, false
	}
	re := regexp.MustCompile(`(?i)REG_DWORD\s+0x([0-9a-f]+)`)
	match := re.FindStringSubmatch(string(output))
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.ParseUint(match[1], 16, 64)
	return value, err == nil
}

func shellExecuteRunAs(executable string, args []string) (int, error) {
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(executable)
	parameters, _ := syscall.UTF16PtrFromString(joinWindowsArgs(args))

	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: parameters,
		nShow:        swShownormal,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	result, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return 0, callErr
	}
	defer procCloseHandle.Call(uintptr(info.hProcess))

	procWaitForSingleObj.Call(uintptr(info.hProcess), infinite)
	var exitCode uint32
	result, _, callErr = procGetExitCode.Call(uintptr(info.hProcess), uintptr(unsafe.Pointer(&exitCode)))
	if result == 0 {
		return 0, callErr
	}
	return int(exitCode), nil
}

func joinWindowsArgs(args []string) string {
	escaped := make([]string, len(args))
	for i, arg := range args {
		escaped[i] = syscall.EscapeArg(arg)
	}
	return strings.Join(escaped, " ")
}

func getHandleExecutable() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", errors.New("LOCALAPPDATA is unavailable")
	}

	name := "handle64.exe"
	if runtime.GOARCH == "386" {
		name = "handle.exe"
	}

	directory := filepath.Join(localAppData, "forcey", "bin")
	path := filepath.Join(directory, name)
	if exists, _ := pathExists(path); exists {
		if verifyAuthenticode(path) == nil {
			return path, nil
		}
		_ = os.Remove(path)
	}

	info(fmt.Sprintf("Downloading Microsoft Sysinternals %s; the final force level is needed.", name))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}

	temporary := path + ".download"
	_ = os.Remove(temporary)
	client := &http.Client{Timeout: 90 * time.Second}
	response, err := client.Get("https://live.sysinternals.com/" + name)
	if err != nil {
		return "", fmt.Errorf("could not download Sysinternals Handle: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Sysinternals download returned HTTP %d", response.StatusCode)
	}

	file, err := os.Create(temporary)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(file, hash), response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return "", closeErr
	}

	if err := verifyAuthenticode(temporary); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("downloaded Sysinternals executable failed signature validation (%s): %w", hex.EncodeToString(hash.Sum(nil)), err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return "", err
	}
	if err := verifyAuthenticode(path); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("cached Sysinternals executable failed final signature validation: %w", err)
	}
	return path, nil
}

func verifyAuthenticode(path string) error {
	escaped := strings.ReplaceAll(path, "'", "''")
	script := fmt.Sprintf("$s=Get-AuthenticodeSignature -LiteralPath '%s'; if($s.Status -eq 'Valid' -and $s.SignerCertificate.Subject -match 'Microsoft'){exit 0}else{exit 1}", escaped)
	for _, shell := range []string{"pwsh.exe", "powershell.exe"} {
		if shellPath, err := exec.LookPath(shell); err == nil {
			cmd := exec.Command(shellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if cmd.Run() == nil {
				return nil
			}
		}
	}
	return errors.New("Microsoft Authenticode signature could not be verified")
}

func findLocks(handleExe string, targets []string) []handleLock {
	re := regexp.MustCompile(`^(.+?)\s+pid:\s*(\d+)\s+type:\s*\S+\s+([0-9A-Fa-f]+):\s*(.+)$`)
	seen := map[string]bool{}
	var locks []handleLock

	for _, target := range targets {
		output, _ := exec.Command(handleExe, "-accepteula", "-nobanner", target).CombinedOutput()
		for _, line := range strings.Split(string(output), "\n") {
			match := re.FindStringSubmatch(strings.TrimSpace(line))
			if len(match) != 5 {
				continue
			}
			pid, err := strconv.Atoi(match[2])
			if err != nil {
				continue
			}
			lockedPath := strings.TrimSpace(match[4])
			if !sameOrInside(lockedPath, target) {
				continue
			}
			key := fmt.Sprintf("%d:%s", pid, strings.ToLower(match[3]))
			if seen[key] {
				continue
			}
			seen[key] = true
			locks = append(locks, handleLock{
				process: strings.TrimSpace(match[1]),
				pid:     pid,
				handle:  match[3],
				path:    lockedPath,
			})
		}
	}

	sort.Slice(locks, func(i, j int) bool {
		if locks[i].pid == locks[j].pid {
			return locks[i].handle < locks[j].handle
		}
		return locks[i].pid < locks[j].pid
	})
	return locks
}

func hasCriticalLock(locks []handleLock) bool {
	critical := map[string]bool{
		"system":       true,
		"registry":     true,
		"smss":         true,
		"smss.exe":     true,
		"csrss":        true,
		"csrss.exe":    true,
		"wininit":      true,
		"wininit.exe":  true,
		"services":     true,
		"services.exe": true,
		"lsass":        true,
		"lsass.exe":    true,
	}
	for _, lock := range locks {
		if critical[strings.ToLower(lock.process)] {
			return true
		}
	}
	return false
}

func closeLocks(handleExe string, locks []handleLock) {
	for _, lock := range locks {
		warn(fmt.Sprintf("Closing handle %s in %s (PID %d)", lock.handle, lock.process, lock.pid))
		cmd := exec.Command(handleExe, "-accepteula", "-nobanner", "-c", lock.handle, "-p", strconv.Itoa(lock.pid), "-y")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
}

func printDeleted(original, remaining []string) {
	remainingSet := map[string]bool{}
	for _, target := range remaining {
		remainingSet[strings.ToLower(target)] = true
	}
	for _, target := range original {
		if !remainingSet[strings.ToLower(target)] {
			fmt.Printf("  + %s\n", target)
		}
	}
}

func printResults(original, remaining []string) {
	remainingSet := map[string]bool{}
	for _, target := range remaining {
		remainingSet[strings.ToLower(target)] = true
	}
	fmt.Println()
	for _, target := range original {
		if remainingSet[strings.ToLower(target)] {
			fmt.Printf("  x %s\n", target)
		} else {
			fmt.Printf("  + %s\n", target)
		}
	}
}

func success(targets []string, level int) int {
	printResults(targets, nil)
	fmt.Printf("✨ forcey deleted everything at force level %d.\n", level)
	return 0
}

func stage(level int, description string) {
	fmt.Println()
	fmt.Printf("[forcey] Level %d/7: %s\n", level, description)
}

func info(message string) {
	fmt.Printf("[forcey] %s\n", message)
}

func warn(message string) {
	fmt.Printf("[forcey] %s\n", message)
}

func fail(message string) {
	fmt.Fprintf(os.Stderr, "[forcey] %s\n", message)
}
