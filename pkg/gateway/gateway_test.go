package gateway

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_StartupFailuresReturnErrorAndEmitStructuredLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		prepare    func(t *testing.T, dir string) string
		wantErr    string
		wantLogSub string
	}{
		{
			name: "invalid config returns load error",
			prepare: func(t *testing.T, dir string) string {
				t.Helper()
				cfgPath := filepath.Join(dir, "invalid-config.json")
				if err := os.WriteFile(cfgPath, []byte("{invalid-json"), 0o644); err != nil {
					t.Fatalf("WriteFile(invalid config) error = %v", err)
				}
				return cfgPath
			},
			wantErr:    "error loading config:",
			wantLogSub: "error loading config:",
		},
		// NOTE: pre-check test case removed — preCheckConfig is from upstream commit 7a1f2ab
		// which was not cherry-picked into halfmoon.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			homeDir := t.TempDir()
			configPath := tt.prepare(t, homeDir)

			cmd := exec.Command(os.Args[0], "-test.run=TestGatewayRunStartupFailureHelper")
			cmd.Env = append(os.Environ(),
				"GO_WANT_GATEWAY_RUN_HELPER=1",
				"PICO_TEST_HOME="+homeDir,
				"PICO_TEST_CONFIG="+configPath,
			)

			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("helper exited unexpectedly: %v\noutput:\n%s", err, string(output))
			}

			out := string(output)
			if !strings.Contains(out, tt.wantErr) {
				t.Fatalf("helper output missing expected error substring %q:\n%s", tt.wantErr, out)
			}

			logData, readErr := os.ReadFile(filepath.Join(homeDir, logPath, logFile))
			if readErr != nil {
				t.Fatalf("ReadFile(gateway.log) error = %v", readErr)
			}
			logText := string(logData)
			if !strings.Contains(logText, "Gateway startup failed") {
				t.Fatalf("gateway.log missing structured startup failure log:\n%s", logText)
			}
			if !strings.Contains(logText, tt.wantLogSub) {
				t.Fatalf("gateway.log missing expected failure detail %q:\n%s", tt.wantLogSub, logText)
			}
		})
	}
}

func TestGatewayRunStartupFailureHelper(t *testing.T) {
	if os.Getenv("GO_WANT_GATEWAY_RUN_HELPER") != "1" {
		return
	}

	homeDir := os.Getenv("PICO_TEST_HOME")
	configPath := os.Getenv("PICO_TEST_CONFIG")

	err := Run(false, homeDir, configPath, false)
	if err == nil {
		fmt.Fprintln(os.Stdout, "expected startup error, got nil")
		os.Exit(2)
	}

	fmt.Fprintln(os.Stdout, err.Error())
	os.Exit(0)
}
