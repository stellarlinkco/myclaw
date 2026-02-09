package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/spf13/cobra"
	"github.com/stellarlinkco/myclaw/internal/config"
	"github.com/stellarlinkco/myclaw/internal/memory"
)

func TestWriteIfNotExists_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	writeIfNotExists(path, "test content")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(data) != "test content" {
		t.Errorf("content = %q, want 'test content'", string(data))
	}
}

func TestWriteIfNotExists_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	// Create existing file
	os.WriteFile(path, []byte("original"), 0644)

	writeIfNotExists(path, "new content")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	// Should not overwrite
	if string(data) != "original" {
		t.Errorf("content = %q, want 'original'", string(data))
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	tmpDir := t.TempDir()

	// Create workspace files
	os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("# Agent\nYou help."), 0644)
	os.WriteFile(filepath.Join(tmpDir, "SOUL.md"), []byte("# Soul\nBe nice."), 0644)

	cfg := &config.Config{
		Agent: config.AgentConfig{
			Workspace: tmpDir,
		},
	}

	mem := memory.NewMemoryStore(tmpDir)

	prompt := buildSystemPrompt(cfg, mem)

	if !strings.Contains(prompt, "# Agent") {
		t.Error("missing AGENTS.md content")
	}
	if !strings.Contains(prompt, "# Soul") {
		t.Error("missing SOUL.md content")
	}
}

func TestBuildSystemPrompt_WithMemory(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			Workspace: tmpDir,
		},
	}

	mem := memory.NewMemoryStore(tmpDir)
	mem.WriteLongTerm("Important info")

	prompt := buildSystemPrompt(cfg, mem)

	if !strings.Contains(prompt, "Important info") {
		t.Error("missing memory content")
	}
}

func TestBuildSystemPrompt_NoFiles(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			Workspace: tmpDir,
		},
	}

	mem := memory.NewMemoryStore(tmpDir)

	prompt := buildSystemPrompt(cfg, mem)

	if prompt != "" {
		t.Errorf("expected empty prompt, got %q", prompt)
	}
}

func TestDefaultConstants(t *testing.T) {
	// Verify default constants are exported in embedded strings
	if !strings.Contains(defaultAgentsMD, "myclaw") {
		t.Error("defaultAgentsMD should mention myclaw")
	}
	if !strings.Contains(defaultSoulMD, "assistant") {
		t.Error("defaultSoulMD should mention assistant")
	}
}

func TestRunOnboard(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runOnboard(&cobra.Command{}, []string{})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Errorf("runOnboard error: %v", err)
	}

	// Check config was created
	cfgPath := filepath.Join(tmpDir, ".myclaw", "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Error("config file was not created")
	}

	// Check workspace was created
	wsPath := filepath.Join(tmpDir, ".myclaw", "workspace")
	if _, err := os.Stat(wsPath); os.IsNotExist(err) {
		t.Error("workspace was not created")
	}

	// Check output contains expected text
	if !strings.Contains(output, "Created config") && !strings.Contains(output, "Config already exists") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestRunOnboard_AlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create existing config
	cfgDir := filepath.Join(tmpDir, ".myclaw")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{}"), 0644)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runOnboard(&cobra.Command{}, []string{})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Errorf("runOnboard error: %v", err)
	}

	// Should say config already exists
	if !strings.Contains(output, "Config already exists") {
		t.Errorf("expected 'Config already exists', got: %s", output)
	}
}

func TestRunStatus(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStatus(&cobra.Command{}, []string{})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Errorf("runStatus error: %v", err)
	}

	// Should contain config info
	if !strings.Contains(output, "Config:") {
		t.Errorf("missing Config in output: %s", output)
	}
	if !strings.Contains(output, "API Key: not set") {
		t.Errorf("missing API Key info in output: %s", output)
	}
	if !strings.Contains(output, "Telegram: enabled=") {
		t.Errorf("missing Telegram status in output: %s", output)
	}
	if !strings.Contains(output, "Feishu: enabled=") {
		t.Errorf("missing Feishu status in output: %s", output)
	}
	if !strings.Contains(output, "WeCom: enabled=") {
		t.Errorf("missing WeCom status in output: %s", output)
	}
}

func TestRunStatus_WithAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Set API key
	t.Setenv("MYCLAW_API_KEY", "sk-ant-test-key-12345678")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStatus(&cobra.Command{}, []string{})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Errorf("runStatus error: %v", err)
	}

	// Should show masked API key
	if !strings.Contains(output, "sk-a...") {
		t.Errorf("API key should be masked in output: %s", output)
	}
}

func TestRunStatus_WithShortAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Set short API key (< 8 chars)
	t.Setenv("MYCLAW_API_KEY", "short")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStatus(&cobra.Command{}, []string{})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Errorf("runStatus error: %v", err)
	}

	// Should show "set" for short key
	if !strings.Contains(output, "API Key: set") {
		t.Errorf("short API key should show 'set': %s", output)
	}
}

func TestRunStatus_WithWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create workspace with memory
	wsDir := filepath.Join(tmpDir, ".myclaw", "workspace", "memory")
	os.MkdirAll(wsDir, 0755)
	os.WriteFile(filepath.Join(wsDir, "MEMORY.md"), []byte("test memory content"), 0644)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStatus(&cobra.Command{}, []string{})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Errorf("runStatus error: %v", err)
	}

	// Should show memory bytes
	if !strings.Contains(output, "Memory:") {
		t.Errorf("missing Memory in output: %s", output)
	}
}

func TestRunStatus_WorkspaceNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create config with non-existent workspace
	cfgDir := filepath.Join(tmpDir, ".myclaw")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"agent":{"workspace":"/nonexistent"}}`), 0644)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStatus(&cobra.Command{}, []string{})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Errorf("runStatus error: %v", err)
	}

	// Should say workspace not found
	if !strings.Contains(output, "not found") {
		t.Errorf("expected 'not found' in output: %s", output)
	}
}

func TestInit(t *testing.T) {
	// Verify init() sets up commands correctly
	if rootCmd == nil {
		t.Error("rootCmd should not be nil")
	}
	if agentCmd == nil {
		t.Error("agentCmd should not be nil")
	}
	if gatewayCmd == nil {
		t.Error("gatewayCmd should not be nil")
	}
	if onboardCmd == nil {
		t.Error("onboardCmd should not be nil")
	}
	if statusCmd == nil {
		t.Error("statusCmd should not be nil")
	}

	// Check message flag exists
	flag := agentCmd.Flags().Lookup("message")
	if flag == nil {
		t.Error("message flag should exist")
	}
}

func TestRunAgent_NoAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	err := runAgent(&cobra.Command{}, []string{})
	if err == nil {
		t.Error("expected error when API key is not set")
	}
	if !strings.Contains(err.Error(), "API key not set") {
		t.Errorf("error should mention API key: %v", err)
	}
}

func TestRunGateway_NoAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	err := runGateway(&cobra.Command{}, []string{})
	if err == nil {
		t.Error("expected error when API key is not set")
	}
	if !strings.Contains(err.Error(), "API key not set") {
		t.Errorf("error should mention API key: %v", err)
	}
}

func TestRunStatus_EmptyMemory(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create workspace with empty memory
	wsDir := filepath.Join(tmpDir, ".myclaw", "workspace", "memory")
	os.MkdirAll(wsDir, 0755)
	os.WriteFile(filepath.Join(wsDir, "MEMORY.md"), []byte(""), 0644)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStatus(&cobra.Command{}, []string{})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if err != nil {
		t.Errorf("runStatus error: %v", err)
	}

	// Should show "Memory: empty" for empty file
	if !strings.Contains(output, "Memory: empty") {
		t.Errorf("expected 'Memory: empty', got: %s", output)
	}
}

// mockRuntime implements Runtime interface for testing
type mockRuntime struct {
	response *api.Response
	err      error
	closed   bool
}

func (m *mockRuntime) Run(ctx context.Context, req api.Request) (*api.Response, error) {
	return m.response, m.err
}

func (m *mockRuntime) Close() {
	m.closed = true
}

// mockRuntimeFactory returns a factory that creates mock runtimes
func mockRuntimeFactory(rt Runtime) RuntimeFactory {
	return func(cfg *config.Config) (Runtime, error) {
		return rt, nil
	}
}

func TestRunAgentWithOptions_SingleMessage(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	mockRt := &mockRuntime{
		response: &api.Response{
			Result: &api.Result{Output: "Hello from mock!"},
		},
	}

	var stdout bytes.Buffer

	// Set messageFlag for single message mode
	oldFlag := messageFlag
	messageFlag = "test message"
	defer func() { messageFlag = oldFlag }()

	err := runAgentWithOptions(AgentOptions{
		RuntimeFactory: mockRuntimeFactory(mockRt),
		Stdout:         &stdout,
	})

	if err != nil {
		t.Errorf("runAgentWithOptions error: %v", err)
	}

	if !strings.Contains(stdout.String(), "Hello from mock!") {
		t.Errorf("expected 'Hello from mock!' in output, got: %s", stdout.String())
	}

	if !mockRt.closed {
		t.Error("runtime should be closed")
	}
}

func TestRunAgentWithOptions_REPLMode(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Clear API key env vars
	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	mockRt := &mockRuntime{
		response: &api.Response{
			Result: &api.Result{Output: "REPL response"},
		},
	}

	// Simulate REPL input: one message then exit
	stdin := strings.NewReader("hello\nexit\n")
	var stdout, stderr bytes.Buffer

	// Clear messageFlag for REPL mode
	oldFlag := messageFlag
	messageFlag = ""
	defer func() { messageFlag = oldFlag }()

	err := runAgentWithOptions(AgentOptions{
		RuntimeFactory: mockRuntimeFactory(mockRt),
		Stdin:          stdin,
		Stdout:         &stdout,
		Stderr:         &stderr,
	})

	if err != nil {
		t.Errorf("runAgentWithOptions error: %v", err)
	}

	if !strings.Contains(stdout.String(), "myclaw agent") {
		t.Errorf("expected REPL welcome message, got: %s", stdout.String())
	}

	if !strings.Contains(stdout.String(), "REPL response") {
		t.Errorf("expected 'REPL response' in output, got: %s", stdout.String())
	}
}

func TestRunAgentWithOptions_REPLMode_EmptyInput(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	mockRt := &mockRuntime{
		response: &api.Response{
			Result: &api.Result{Output: "response"},
		},
	}

	// Empty lines should be skipped
	stdin := strings.NewReader("\n\nhello\nquit\n")
	var stdout bytes.Buffer

	oldFlag := messageFlag
	messageFlag = ""
	defer func() { messageFlag = oldFlag }()

	err := runAgentWithOptions(AgentOptions{
		RuntimeFactory: mockRuntimeFactory(mockRt),
		Stdin:          stdin,
		Stdout:         &stdout,
	})

	if err != nil {
		t.Errorf("error: %v", err)
	}
}

func TestRunAgentWithOptions_REPLMode_Error(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	mockRt := &mockRuntime{
		err: context.DeadlineExceeded,
	}

	stdin := strings.NewReader("hello\nexit\n")
	var stdout, stderr bytes.Buffer

	oldFlag := messageFlag
	messageFlag = ""
	defer func() { messageFlag = oldFlag }()

	err := runAgentWithOptions(AgentOptions{
		RuntimeFactory: mockRuntimeFactory(mockRt),
		Stdin:          stdin,
		Stdout:         &stdout,
		Stderr:         &stderr,
	})

	if err != nil {
		t.Errorf("error: %v", err)
	}

	// Error should be written to stderr
	if !strings.Contains(stderr.String(), "Error:") {
		t.Errorf("expected error in stderr, got: %s", stderr.String())
	}
}

func TestRunAgentWithOptions_SingleMessage_Error(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	mockRt := &mockRuntime{
		err: context.DeadlineExceeded,
	}

	oldFlag := messageFlag
	messageFlag = "test"
	defer func() { messageFlag = oldFlag }()

	err := runAgentWithOptions(AgentOptions{
		RuntimeFactory: mockRuntimeFactory(mockRt),
	})

	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "agent error") {
		t.Errorf("expected 'agent error', got: %v", err)
	}
}

func TestRunAgentWithOptions_NilResult(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	t.Setenv("MYCLAW_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")

	mockRt := &mockRuntime{
		response: &api.Response{Result: nil},
	}

	var stdout bytes.Buffer

	oldFlag := messageFlag
	messageFlag = "test"
	defer func() { messageFlag = oldFlag }()

	err := runAgentWithOptions(AgentOptions{
		RuntimeFactory: mockRuntimeFactory(mockRt),
		Stdout:         &stdout,
	})

	if err != nil {
		t.Errorf("error: %v", err)
	}
}

func TestDefaultRuntimeFactory_NoAPIKey(t *testing.T) {
	cfg := &config.Config{
		Provider: config.ProviderConfig{
			APIKey: "",
		},
	}

	_, err := DefaultRuntimeFactory(cfg)
	if err == nil {
		t.Error("expected error when API key is not set")
	}
	if !strings.Contains(err.Error(), "API key not set") {
		t.Errorf("error should mention API key: %v", err)
	}
}
