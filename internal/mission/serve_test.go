package mission

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCLIServeHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := RunCLI([]string{"serve", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "bridge-port") {
		t.Fatalf("help output missing serve flags: %s", stderr.String())
	}
}

func TestServeProcessSpecsLocal(t *testing.T) {
	options := defaultServeOptions()
	options.local = true
	options.codexHome = "/tmp/codex-home"
	options.projectRoot = "/tmp/repos"
	options.bridgePort = 18765
	options.zukoPort = 19777

	specs, err := serveProcessSpecs(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("spec count = %d, want 2", len(specs))
	}
	if specs[0].url != "http://127.0.0.1:18765" {
		t.Fatalf("bridge url = %q", specs[0].url)
	}
	if specs[1].url != "http://127.0.0.1:19777" {
		t.Fatalf("zuko url = %q", specs[1].url)
	}
	if !containsArg(specs[0].args, "--local") {
		t.Fatalf("bridge worker serve args missing --local: %#v", specs[0].args)
	}
	if !containsArg(specs[0].args, "--bridge-port") {
		t.Fatalf("bridge worker serve args missing --bridge-port: %#v", specs[0].args)
	}
	if containsArg(specs[0].args, "--tailscale") {
		t.Fatalf("local bridge worker serve args should not include --tailscale: %#v", specs[0].args)
	}
	if containsArg(specs[1].args, "--tailscale") {
		t.Fatalf("local zuko args should not include --tailscale: %#v", specs[1].args)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
