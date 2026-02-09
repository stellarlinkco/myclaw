package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/spf13/cobra"
	"github.com/stellarlinkco/myclaw/internal/config"
	"github.com/stellarlinkco/myclaw/internal/gateway"
	"github.com/stellarlinkco/myclaw/internal/memory"
)

// Runtime interface for agent runtime (allows mocking in tests)
type Runtime interface {
	Run(ctx context.Context, req api.Request) (*api.Response, error)
	Close()
}

// runtimeWrapper wraps api.Runtime to implement Runtime interface
type runtimeWrapper struct {
	rt *api.Runtime
}

func (r *runtimeWrapper) Run(ctx context.Context, req api.Request) (*api.Response, error) {
	return r.rt.Run(ctx, req)
}

func (r *runtimeWrapper) Close() {
	r.rt.Close()
}

// RuntimeFactory creates a Runtime instance
type RuntimeFactory func(cfg *config.Config) (Runtime, error)

// DefaultRuntimeFactory creates the default agentsdk-go runtime
func DefaultRuntimeFactory(cfg *config.Config) (Runtime, error) {
	if cfg.Provider.APIKey == "" {
		return nil, fmt.Errorf("API key not set. Run 'myclaw onboard' or set MYCLAW_API_KEY / ANTHROPIC_API_KEY")
	}

	mem := memory.NewMemoryStore(cfg.Agent.Workspace)
	sysPrompt := buildSystemPrompt(cfg, mem)

	var provider api.ModelFactory
	switch cfg.Provider.Type {
	case "openai":
		provider = &model.OpenAIProvider{
			APIKey:    cfg.Provider.APIKey,
			BaseURL:   cfg.Provider.BaseURL,
			ModelName: cfg.Agent.Model,
			MaxTokens: cfg.Agent.MaxTokens,
		}
	default:
		provider = &model.AnthropicProvider{
			APIKey:    cfg.Provider.APIKey,
			BaseURL:   cfg.Provider.BaseURL,
			ModelName: cfg.Agent.Model,
			MaxTokens: cfg.Agent.MaxTokens,
		}
	}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:   cfg.Agent.Workspace,
		ModelFactory:  provider,
		SystemPrompt:  sysPrompt,
		MaxIterations: cfg.Agent.MaxToolIterations,
	})
	if err != nil {
		return nil, fmt.Errorf("create runtime: %w", err)
	}
	return &runtimeWrapper{rt: rt}, nil
}

// AgentOptions for running agent with custom dependencies
type AgentOptions struct {
	RuntimeFactory RuntimeFactory
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

var rootCmd = &cobra.Command{
	Use:   "myclaw",
	Short: "myclaw - personal AI assistant",
}

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Run agent in single message or REPL mode",
	RunE:  runAgent,
}

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Start the full gateway (channels + cron + heartbeat)",
	RunE:  runGateway,
}

var onboardCmd = &cobra.Command{
	Use:   "onboard",
	Short: "Initialize config and workspace",
	RunE:  runOnboard,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show myclaw status",
	RunE:  runStatus,
}

var messageFlag string

func init() {
	agentCmd.Flags().StringVarP(&messageFlag, "message", "m", "", "Single message to send")
	rootCmd.AddCommand(agentCmd, gatewayCmd, onboardCmd, statusCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runAgent is the command handler that uses default options
func runAgent(cmd *cobra.Command, args []string) error {
	return runAgentWithOptions(AgentOptions{})
}

// runAgentWithOptions runs the agent with injectable dependencies for testing
func runAgentWithOptions(opts AgentOptions) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Use injected factory or default
	factory := opts.RuntimeFactory
	if factory == nil {
		factory = DefaultRuntimeFactory
	}

	rt, err := factory(cfg)
	if err != nil {
		return err
	}
	defer rt.Close()

	// Use injected IO or defaults
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	ctx := context.Background()

	// Single message mode
	if messageFlag != "" {
		resp, err := rt.Run(ctx, api.Request{
			Prompt:    messageFlag,
			SessionID: "cli",
		})
		if err != nil {
			return fmt.Errorf("agent error: %w", err)
		}
		if resp != nil && resp.Result != nil {
			fmt.Fprintln(stdout, resp.Result.Output)
		}
		return nil
	}

	// REPL mode
	fmt.Fprintln(stdout, "myclaw agent (type 'exit' to quit)")
	scanner := bufio.NewScanner(stdin)
	for {
		fmt.Fprint(stdout, "\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}

		resp, err := rt.Run(ctx, api.Request{
			Prompt:    input,
			SessionID: "cli-repl",
		})
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			continue
		}
		if resp != nil && resp.Result != nil {
			fmt.Fprintln(stdout, resp.Result.Output)
		}
	}
	return nil
}

func runGateway(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.Provider.APIKey == "" {
		return fmt.Errorf("API key not set. Run 'myclaw onboard' or set MYCLAW_API_KEY / ANTHROPIC_API_KEY")
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		return fmt.Errorf("create gateway: %w", err)
	}

	return gw.Run(context.Background())
}

func runOnboard(cmd *cobra.Command, args []string) error {
	cfgDir := config.ConfigDir()
	cfgPath := config.ConfigPath()

	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		cfg := config.DefaultConfig()
		data, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(cfgPath, data, 0644); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		fmt.Printf("Created config: %s\n", cfgPath)
	} else {
		fmt.Printf("Config already exists: %s\n", cfgPath)
	}

	cfg, _ := config.LoadConfig()
	ws := cfg.Agent.Workspace
	if err := os.MkdirAll(filepath.Join(ws, "memory"), 0755); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}

	writeIfNotExists(filepath.Join(ws, "AGENTS.md"), defaultAgentsMD)
	writeIfNotExists(filepath.Join(ws, "SOUL.md"), defaultSoulMD)
	writeIfNotExists(filepath.Join(ws, "memory", "MEMORY.md"), "")
	writeIfNotExists(filepath.Join(ws, "HEARTBEAT.md"), "")

	fmt.Printf("Workspace ready: %s\n", ws)
	fmt.Println("\nNext steps:")
	fmt.Printf("  1. Edit %s to set your API key\n", cfgPath)
	fmt.Println("  2. Or set MYCLAW_API_KEY environment variable")
	fmt.Println("  3. Run 'myclaw agent -m \"Hello\"' to test")

	return nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Printf("Config: error (%v)\n", err)
		return nil
	}

	fmt.Printf("Config: %s\n", config.ConfigPath())
	fmt.Printf("Workspace: %s\n", cfg.Agent.Workspace)
	fmt.Printf("Model: %s\n", cfg.Agent.Model)
	fmt.Printf("Provider: %s\n", providerDisplay(cfg.Provider.Type))
	if cfg.Provider.APIKey != "" && len(cfg.Provider.APIKey) > 8 {
		masked := cfg.Provider.APIKey[:4] + "..." + cfg.Provider.APIKey[len(cfg.Provider.APIKey)-4:]
		fmt.Printf("API Key: %s\n", masked)
	} else if cfg.Provider.APIKey != "" {
		fmt.Println("API Key: set")
	} else {
		fmt.Println("API Key: not set")
	}
	fmt.Printf("Telegram: enabled=%v\n", cfg.Channels.Telegram.Enabled)
	fmt.Printf("Feishu: enabled=%v\n", cfg.Channels.Feishu.Enabled)
	fmt.Printf("WeCom: enabled=%v\n", cfg.Channels.WeCom.Enabled)

	if _, err := os.Stat(cfg.Agent.Workspace); err != nil {
		fmt.Println("Workspace: not found (run 'myclaw onboard')")
	} else {
		mem := memory.NewMemoryStore(cfg.Agent.Workspace)
		lt, _ := mem.ReadLongTerm()
		if lt != "" {
			fmt.Printf("Memory: %d bytes\n", len(lt))
		} else {
			fmt.Println("Memory: empty")
		}
	}

	return nil
}

func providerDisplay(t string) string {
	if t == "" {
		return "anthropic (default)"
	}
	return t
}

func buildSystemPrompt(cfg *config.Config, mem *memory.MemoryStore) string {
	var sb strings.Builder

	if data, err := os.ReadFile(filepath.Join(cfg.Agent.Workspace, "AGENTS.md")); err == nil {
		sb.Write(data)
		sb.WriteString("\n\n")
	}

	if data, err := os.ReadFile(filepath.Join(cfg.Agent.Workspace, "SOUL.md")); err == nil {
		sb.Write(data)
		sb.WriteString("\n\n")
	}

	if memCtx := mem.GetMemoryContext(); memCtx != "" {
		sb.WriteString(memCtx)
	}

	return sb.String()
}

func writeIfNotExists(path, content string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte(content), 0644)
		fmt.Printf("  Created: %s\n", path)
	}
}

const defaultAgentsMD = `# myclaw Agent

You are myclaw, a personal AI assistant.

You have access to tools for file operations, web search, and command execution.
Use them to help the user accomplish tasks.

## Guidelines
- Be concise and helpful
- Use tools proactively when needed
- Remember information the user tells you by writing to memory
- Check your memory context for previously stored information
`

const defaultSoulMD = `# Soul

You are a capable personal assistant that helps with daily tasks,
research, coding, and general questions.

Your personality:
- Direct and efficient
- Technical when needed, simple when possible
- Proactive about using tools to get real answers
`
