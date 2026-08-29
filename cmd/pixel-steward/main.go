package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/samcm/pixel-steward/internal/agent"
	"github.com/samcm/pixel-steward/internal/api"
	"github.com/samcm/pixel-steward/internal/config"
	"github.com/samcm/pixel-steward/internal/controller"
	"github.com/samcm/pixel-steward/internal/display"
	"github.com/samcm/pixel-steward/internal/executor"
	"github.com/samcm/pixel-steward/internal/mcp"
	"github.com/samcm/pixel-steward/internal/objectstore"
	"github.com/samcm/pixel-steward/internal/runtime"
	"github.com/samcm/pixel-steward/internal/store"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("pixel-steward stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	arguments := os.Args[1:]
	if len(os.Args) > 1 && os.Args[1][0] != '-' {
		command = os.Args[1]
		arguments = os.Args[2:]
	}
	switch command {
	case "serve":
		return serve(arguments)
	case "mcp":
		return serveMCP(arguments)
	case "executor":
		return serveExecutor(arguments)
	case "version", "--version", "-version":
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (expected serve, mcp, executor, or version)", command)
	}
}

func serve(arguments []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", "config.yaml", "configuration file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := buildStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer database.Close()
	objects, err := buildObjects(ctx, cfg)
	if err != nil {
		return err
	}
	panel, err := buildDisplay(cfg)
	if err != nil {
		return err
	}
	sandbox, err := buildSandbox(cfg)
	if err != nil {
		return err
	}
	var runner agent.Runner = agent.Disabled{}
	if cfg.Runtime.Driver == "opencode" || cfg.Runtime.Driver == "hermes" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		if cfg.Runtime.Driver == "hermes" {
			runner, err = runtime.NewHermes(cfg.Runtime, database, executable)
		} else {
			runner, err = runtime.NewOpenCode(cfg.Runtime, database, executable)
		}
		if err != nil {
			return err
		}
	}
	service, err := controller.New(cfg, database, objects, panel, runner, sandbox, nil)
	if err != nil {
		return err
	}
	apiServer, err := api.NewServer(service, cfg.HTTP)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: cfg.HTTP.Listen, Handler: apiServer.Handler(), ReadHeaderTimeout: 10 * time.Second}
	controllerDone := make(chan error, 1)
	go func() { controllerDone <- service.Run(ctx) }()
	serverDone := make(chan error, 1)
	go func() {
		slog.Info("pixel-steward listening", "address", cfg.HTTP.Listen)
		serverDone <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return errors.Join(api.Shutdown(shutdown, httpServer), normalizeServerError(<-serverDone))
	case err := <-serverDone:
		stop()
		return normalizeServerError(err)
	case err := <-controllerDone:
		stop()
		return normalizeContextError(err)
	}
}

func serveMCP(arguments []string) error {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	apiURL := flags.String("api", "", "Pixel Steward controller URL")
	token := flags.String("token", "", "lease-scoped agent token")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *apiURL == "" || *token == "" {
		return errors.New("--api and --token are required")
	}
	return mcp.New(*apiURL, *token).Serve(context.Background())
}

func serveExecutor(arguments []string) error {
	flags := flag.NewFlagSet("executor", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:8081", "listen address")
	root := flags.String("root", "./data/sandboxes", "sandbox workspace root")
	tokenEnv := flags.String("token-env", "PIXEL_STEWARD_EXECUTOR_TOKEN", "environment variable holding the shared controller token")
	maxExec := flags.Duration("max-exec-time", 10*time.Minute, "maximum command runtime")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	token := os.Getenv(*tokenEnv)
	if token == "" {
		return fmt.Errorf("executor token environment %s is empty", *tokenEnv)
	}
	backend, err := executor.NewLocal(*root, *maxExec)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *listen, Handler: executor.NewHTTPServer(token, backend), ReadHeaderTimeout: 10 * time.Second}
	slog.Info("sandbox executor listening", "address", *listen)
	return normalizeServerError(server.ListenAndServe())
}

func buildStore(ctx context.Context, cfg config.Config) (store.Store, error) {
	if cfg.Database.Driver == "postgres" {
		return store.NewPostgres(ctx, cfg.Database.URL)
	}
	return store.NewMemory(), nil
}

func buildObjects(ctx context.Context, cfg config.Config) (objectstore.Store, error) {
	if cfg.Storage.Driver == "s3" {
		return objectstore.NewS3(ctx, objectstore.S3Config{Endpoint: cfg.Storage.Endpoint, Region: cfg.Storage.Region,
			Bucket: cfg.Storage.Bucket, AccessKey: os.Getenv(cfg.Storage.AccessKeyEnv), SecretKey: os.Getenv(cfg.Storage.SecretKeyEnv), UseTLS: cfg.Storage.UseTLS})
	}
	return objectstore.NewFilesystem(cfg.Storage.Directory)
}

func buildDisplay(cfg config.Config) (display.Display, error) {
	if cfg.Display.Adapter == "http" {
		return display.NewHTTP(cfg.Display.BaseURL, cfg.Display.MaxFPS)
	}
	return display.NewFake(), nil
}

func buildSandbox(cfg config.Config) (executor.Executor, error) {
	switch cfg.Sandbox.Driver {
	case "local":
		return executor.NewLocal(cfg.Sandbox.LocalRoot, cfg.Sandbox.MaxExecTime.Duration())
	case "http":
		return executor.NewHTTP(cfg.Sandbox.BaseURL, os.Getenv(cfg.Sandbox.TokenEnv))
	case "command":
		return executor.NewCommand(executor.CommandConfig{ExecCommand: cfg.Sandbox.ExecCommand, ReadCommand: cfg.Sandbox.ReadCommand,
			SuspendCommand: cfg.Sandbox.SuspendCommand, ResumeCommand: cfg.Sandbox.ResumeCommand,
			ResetCommand: cfg.Sandbox.ResetCommand, MaxRuntime: cfg.Sandbox.MaxExecTime.Duration()})
	default:
		return executor.Disabled{}, nil
	}
}

func normalizeServerError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func normalizeContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
