package mission

import (
	"encoding/json"
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
	"strconv"
	"syscall"
	"time"
)

const (
	defaultZukoPort         = 9777
	cmcBridgeWorkerEnv      = "CMC_SERVE_BRIDGE_WORKER"
	cmcServeHealthWait      = 5 * time.Second
	cmcServeHealthProbeTime = 500 * time.Millisecond
)

type serveOptions struct {
	codexHome   string
	projectRoot string
	limit       int
	bridgePort  int
	zukoPort    int
	local       bool
	foreground  bool
	noZuko      bool
	zukoBin     string
	status      bool
	stop        bool
}

type serveServiceState struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	PID            int    `json:"pid,omitempty"`
	LogPath        string `json:"log_path,omitempty"`
	AlreadyRunning bool   `json:"already_running,omitempty"`
}

type serveState struct {
	StartedAt time.Time           `json:"started_at"`
	Services  []serveServiceState `json:"services"`
}

type serveProcessSpec struct {
	name string
	path string
	args []string
	env  []string
	url  string
	log  string
}

func RunServe(args []string, stdout, stderr io.Writer) int {
	options := defaultServeOptions()
	flags := flag.NewFlagSet("cmc serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.codexHome, "codex-home", options.codexHome, "Codex home directory")
	flags.StringVar(&options.projectRoot, "projects-root", options.projectRoot, "root directory for new-chat project picker")
	flags.IntVar(&options.limit, "limit", options.limit, "maximum threads to load")
	flags.IntVar(&options.bridgePort, "bridge-port", options.bridgePort, "Codex bridge HTTP port")
	flags.IntVar(&options.zukoPort, "zuko-port", options.zukoPort, "Zuko approval HTTP port")
	flags.BoolVar(&options.local, "local", options.local, "bind to 127.0.0.1 instead of this Mac's Tailscale IPv4 address")
	flags.BoolVar(&options.foreground, "foreground", options.foreground, "run services in the foreground")
	flags.BoolVar(&options.noZuko, "no-zuko", options.noZuko, "start only the Codex bridge")
	flags.StringVar(&options.zukoBin, "zuko-bin", options.zukoBin, "zuko binary to launch")
	flags.BoolVar(&options.status, "status", options.status, "show background service status")
	flags.BoolVar(&options.stop, "stop", options.stop, "stop background services from the last cmc serve run")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	if os.Getenv(cmcBridgeWorkerEnv) == "1" {
		return runServeBridgeWorker(options, stdout, stderr)
	}
	if options.stop {
		return stopServeServices(stdout, stderr)
	}
	if options.status {
		return printServeStatus(stdout, stderr)
	}

	if err := validateServeOptions(options); err != nil {
		fmt.Fprintf(stderr, "cmc serve: %v\n", err)
		return 2
	}

	specs, err := serveProcessSpecs(options)
	if err != nil {
		fmt.Fprintf(stderr, "cmc serve: %v\n", err)
		return 1
	}

	if options.foreground {
		return runServeForeground(specs, stdout, stderr)
	}
	return runServeBackground(specs, stdout, stderr)
}

func defaultServeOptions() serveOptions {
	home, _ := os.UserHomeDir()
	return serveOptions{
		codexHome:   filepath.Join(home, ".codex"),
		projectRoot: defaultProjectsRoot(),
		limit:       80,
		bridgePort:  defaultBridgePort,
		zukoPort:    defaultZukoPort,
		zukoBin:     "zuko",
	}
}

func validateServeOptions(options serveOptions) error {
	if options.bridgePort <= 0 || options.bridgePort > 65535 {
		return fmt.Errorf("invalid bridge port: %d", options.bridgePort)
	}
	if !options.noZuko && (options.zukoPort <= 0 || options.zukoPort > 65535) {
		return fmt.Errorf("invalid zuko port: %d", options.zukoPort)
	}
	return nil
}

func serveProcessSpecs(options serveOptions) ([]serveProcessSpec, error) {
	bridgeURL, err := serveURL(options.local, options.bridgePort)
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate cmc executable: %w", err)
	}

	specs := []serveProcessSpec{{
		name: "Codex bridge",
		path: executable,
		args: append([]string{"serve"}, bridgeWorkerServeArgs(options)...),
		env:  []string{cmcBridgeWorkerEnv + "=1"},
		url:  bridgeURL,
		log:  "codex-bridge.log",
	}}

	if !options.noZuko {
		zukoURL, err := serveURL(options.local, options.zukoPort)
		if err != nil {
			return nil, err
		}
		zukoArgs := []string{"serve", "--addr", net.JoinHostPort("127.0.0.1", strconv.Itoa(options.zukoPort))}
		if !options.local {
			zukoArgs = append(zukoArgs, "--tailscale")
		}
		specs = append(specs, serveProcessSpec{
			name: "Zuko approvals",
			path: options.zukoBin,
			args: zukoArgs,
			url:  zukoURL,
			log:  "zuko-approvals.log",
		})
	}

	return specs, nil
}

func bridgeWorkerServeArgs(options serveOptions) []string {
	args := []string{
		"--codex-home", options.codexHome,
		"--limit", strconv.Itoa(options.limit),
		"--projects-root", options.projectRoot,
		"--bridge-port", strconv.Itoa(options.bridgePort),
	}
	if options.local {
		args = append(args, "--local")
	}
	return args
}

func bridgeWorkerArgs(options serveOptions) []string {
	args := []string{
		"--codex-home", options.codexHome,
		"--limit", strconv.Itoa(options.limit),
		"--projects-root", options.projectRoot,
	}
	if options.local {
		return append(args, "--addr", net.JoinHostPort("127.0.0.1", strconv.Itoa(options.bridgePort)))
	}
	return append(args, "--tailscale", "--port", strconv.Itoa(options.bridgePort))
}

func runServeBridgeWorker(options serveOptions, stdout, stderr io.Writer) int {
	return RunBridge(bridgeWorkerArgs(options), stdout, stderr)
}

func runServeBackground(specs []serveProcessSpec, stdout, stderr io.Writer) int {
	logDir, err := serveLogDir()
	if err != nil {
		fmt.Fprintf(stderr, "cmc serve: %v\n", err)
		return 1
	}

	var states []serveServiceState
	var started []*os.Process
	for _, spec := range specs {
		if healthOK(spec.url) {
			states = append(states, serveServiceState{
				Name:           spec.name,
				URL:            spec.url,
				AlreadyRunning: true,
			})
			continue
		}

		state, process, err := startBackgroundService(spec, logDir)
		if err != nil {
			killProcesses(started)
			fmt.Fprintf(stderr, "cmc serve: %v\n", err)
			return 1
		}
		started = append(started, process)
		states = append(states, state)
	}

	for _, state := range states {
		if err := waitForHealth(state.URL, cmcServeHealthWait); err != nil {
			killProcesses(started)
			fmt.Fprintf(stderr, "cmc serve: %s did not become healthy at %s: %v\n", state.Name, state.URL, err)
			if state.LogPath != "" {
				fmt.Fprintf(stderr, "cmc serve: see %s\n", state.LogPath)
			}
			return 1
		}
	}
	releaseProcesses(started)

	if err := saveServeState(serveState{StartedAt: time.Now(), Services: states}); err != nil {
		fmt.Fprintf(stderr, "cmc serve: failed to save serve state: %v\n", err)
	}

	fmt.Fprintln(stdout, "CMC services are running in the background.")
	for _, state := range states {
		status := "started"
		if state.AlreadyRunning {
			status = "already running"
		}
		if state.PID > 0 {
			fmt.Fprintf(stdout, "- %s: %s (%s, pid %d)\n", state.Name, state.URL, status, state.PID)
		} else {
			fmt.Fprintf(stdout, "- %s: %s (%s)\n", state.Name, state.URL, status)
		}
		if state.LogPath != "" {
			fmt.Fprintf(stdout, "  log: %s\n", state.LogPath)
		}
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Use these URLs in the iOS app:")
	for _, state := range states {
		fmt.Fprintf(stdout, "- %s URL: %s\n", state.Name, state.URL)
	}
	return 0
}

func runServeForeground(specs []serveProcessSpec, stdout, stderr io.Writer) int {
	var processes []*os.Process
	for _, spec := range specs {
		if healthOK(spec.url) {
			fmt.Fprintf(stdout, "%s already running at %s\n", spec.name, spec.url)
			continue
		}
		cmd := exec.Command(spec.path, spec.args...)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Env = append(os.Environ(), spec.env...)
		if err := cmd.Start(); err != nil {
			killProcesses(processes)
			fmt.Fprintf(stderr, "cmc serve: start %s: %v\n", spec.name, err)
			return 1
		}
		processes = append(processes, cmd.Process)
		go cmd.Wait()
		fmt.Fprintf(stdout, "%s starting at %s (pid %d)\n", spec.name, spec.url, cmd.Process.Pid)
	}
	if len(processes) == 0 {
		return 0
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	killProcesses(processes)
	return 0
}

func startBackgroundService(spec serveProcessSpec, logDir string) (serveServiceState, *os.Process, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return serveServiceState{}, nil, err
	}
	logPath := filepath.Join(logDir, spec.log)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return serveServiceState{}, nil, err
	}
	defer logFile.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return serveServiceState{}, nil, err
	}
	defer devNull.Close()

	cmd := exec.Command(spec.path, spec.args...)
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), spec.env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return serveServiceState{}, nil, fmt.Errorf("start %s: %w", spec.name, err)
	}
	process := cmd.Process
	return serveServiceState{
		Name:    spec.name,
		URL:     spec.url,
		PID:     process.Pid,
		LogPath: logPath,
	}, process, nil
}

func serveURL(local bool, port int) (string, error) {
	if local {
		return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
	}
	addr, err := tailscaleBridgeAddr(port)
	if err != nil {
		return "", err
	}
	return "http://" + addr, nil
}

func healthOK(baseURL string) bool {
	client := http.Client{Timeout: cmcServeHealthProbeTime}
	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func waitForHealth(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if healthOK(baseURL) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for /health")
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func killProcesses(processes []*os.Process) {
	for _, process := range processes {
		if process != nil {
			_ = process.Kill()
		}
	}
}

func releaseProcesses(processes []*os.Process) {
	for _, process := range processes {
		if process != nil {
			_ = process.Release()
		}
	}
}

func cmcConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "codex-mission-control"), nil
}

func serveLogDir() (string, error) {
	dir, err := cmcConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs"), nil
}

func saveServeState(state serveState) error {
	dir, err := cmcConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "serve.json"), data, 0o600)
}

func loadServeState() (serveState, error) {
	dir, err := cmcConfigDir()
	if err != nil {
		return serveState{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "serve.json"))
	if err != nil {
		return serveState{}, err
	}
	var state serveState
	if err := json.Unmarshal(data, &state); err != nil {
		return serveState{}, err
	}
	return state, nil
}

func printServeStatus(stdout, stderr io.Writer) int {
	state, err := loadServeState()
	if err != nil {
		fmt.Fprintf(stderr, "cmc serve: no saved service state: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "CMC services from %s:\n", state.StartedAt.Format(time.RFC3339))
	for _, service := range state.Services {
		status := "down"
		if healthOK(service.URL) {
			status = "healthy"
		}
		if service.PID > 0 {
			fmt.Fprintf(stdout, "- %s: %s (%s, pid %d)\n", service.Name, service.URL, status, service.PID)
		} else {
			fmt.Fprintf(stdout, "- %s: %s (%s)\n", service.Name, service.URL, status)
		}
	}
	return 0
}

func stopServeServices(stdout, stderr io.Writer) int {
	state, err := loadServeState()
	if err != nil {
		fmt.Fprintf(stderr, "cmc serve: no saved service state: %v\n", err)
		return 1
	}

	exitCode := 0
	for _, service := range state.Services {
		if service.PID <= 0 {
			fmt.Fprintf(stdout, "- %s: no saved pid\n", service.Name)
			continue
		}
		process, err := os.FindProcess(service.PID)
		if err != nil {
			fmt.Fprintf(stderr, "cmc serve: find %s pid %d: %v\n", service.Name, service.PID, err)
			exitCode = 1
			continue
		}
		if err := process.Signal(syscall.SIGTERM); err != nil {
			fmt.Fprintf(stderr, "cmc serve: stop %s pid %d: %v\n", service.Name, service.PID, err)
			exitCode = 1
			continue
		}
		fmt.Fprintf(stdout, "- %s: stopped pid %d\n", service.Name, service.PID)
	}
	return exitCode
}
