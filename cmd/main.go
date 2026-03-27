// Command marti runs the cryptocurrency trading bot with DCA strategy.
// It supports multiple exchanges (Binance, Bybit, Hyperliquid) and can be configured
// via YAML configuration files or command-line arguments.
//
// By default marti starts with web UI. Use --cli to run without it.
//
// Usage:
//
//	marti                       (web UI, opens browser)
//	marti --config config.yaml  (web UI with explicit config)
//	marti --cli                 (CLI-only, TUI wizard if no config)
//
// Required environment variables:
//
//	For Binance: BINANCE_API_KEY, BINANCE_API_SECRET
//	For Bybit: BYBIT_API_KEY, BYBIT_API_SECRET
//	For Hyperliquid: HYPERLIQ_PRIVATE_KEY (hex), optional HYPERLIQ_API_URL
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/pkg/errors"
	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/dashboard"
	"github.com/vadiminshakov/marti/internal/repository/balancesnapshots"
	"github.com/vadiminshakov/marti/internal/repository/decisions"
	"github.com/vadiminshakov/marti/pkg/setup"
	"github.com/vadiminshakov/marti/pkg/telegram"
	"go.uber.org/zap"
)

func main() {
	var webAddr string
	var tlsDomainsArg string
	var tlsCacheDir string
	var cliMode bool
	var configMissing bool

	flag.StringVar(&webAddr, "web", ":8000", "address for web UI")
	flag.StringVar(&tlsDomainsArg, "tls-domain", "", "comma-separated list of domains for automatic TLS via ACME (e.g. Let's Encrypt); requires ports 80 and 443")
	flag.StringVar(&tlsCacheDir, "tls-cache-dir", "cert-cache", "directory to cache automatic TLS certificates")
	flag.BoolVar(&cliMode, "cli", false, "run without web UI (CLI-only mode)")
	flag.Parse()

	// resolve config: explicit --config flag -> config.gen.yaml fallback.
	configFlag := flag.Lookup("config")
	configMissing = configFlag == nil || configFlag.Value.String() == ""

	if configMissing {
		if _, err := os.Stat("config.gen.yaml"); err == nil {
			if err := flag.Set("config", "config.gen.yaml"); err != nil {
				logger.Fatal("Failed to set config flag", zap.Error(err))
			}
			configMissing = false
		}
	}

	// in CLI mode without config, run the TUI wizard.
	if cliMode && configMissing {
		logger.Info("No config found, starting interactive setup...")
		if err := setup.RunTUI(); err != nil {
			logger.Fatal("Setup failed", zap.Error(err))
		}

		if err := flag.Set("config", "config.gen.yaml"); err != nil {
			logger.Fatal("Failed to set config flag after setup", zap.Error(err))
		}
		configMissing = false
	}

	var tlsDomains []string
	if tlsDomainsArg != "" {
		for _, d := range strings.Split(tlsDomainsArg, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				tlsDomains = append(tlsDomains, d)
			}
		}
	}
	cfg := zap.NewProductionConfig()
	cfg.DisableStacktrace = true
	logger := zap.Must(cfg.Build())
	defer func() {
		_ = logger.Sync()
	}()

	var configs []config.Config
	if !configMissing {
		var err error
		configs, err = config.Get()
		if err != nil {
			logger.Fatal("Failed to load configuration", zap.Error(err))
		}
	}

	snapshotStore, err := balancesnapshots.NewWALStore("")
	if err != nil {
		logger.Fatal("Failed to initialize balance snapshot store", zap.Error(err))
	}
	defer func() {
		if err := snapshotStore.Close(); err != nil {
			logger.Warn("Failed to close snapshot store", zap.Error(err))
		}
	}()

	rawDecisionStore, err := decisions.NewWALStore("")
	if err != nil {
		logger.Fatal("Failed to initialize decision store", zap.Error(err))
	}
	defer func() {
		if err := rawDecisionStore.Close(); err != nil {
			logger.Warn("Failed to close decision store", zap.Error(err))
		}
	}()

	var tgToken, tgChatID string
	for _, c := range configs {
		if c.TelegramBotToken != "" && c.TelegramChatID != "" {
			tgToken = c.TelegramBotToken
			tgChatID = c.TelegramChatID
			break
		}
	}
	decisionStore := decisions.NewNotifyingStore(rawDecisionStore, telegram.New(tgToken, tgChatID), logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case sig := <-sigChan:
			logger.Info("Received shutdown signal, initiating graceful shutdown...", zap.String("signal", sig.String()))
		case <-ctx.Done():
			return
		}
		cancel()
	}()

	var wg sync.WaitGroup
	manager := newBotManager(ctx, logger, snapshotStore, decisionStore)
	if err := manager.ApplyConfigs(configs); err != nil {
		logger.Fatal("Failed to start trading bots", zap.Error(err))
	}

	// start web UI unless --cli mode is enabled.
	var srv *dashboard.Server
	if !cliMode {
		onSetupSaved := func(configPath string) error {
			if err := flag.Set("config", configPath); err != nil {
				return errors.Wrap(err, "failed to set config path")
			}

			reloadedConfigs, err := config.Get()
			if err != nil {
				return errors.Wrap(err, "failed to load saved config")
			}

			if err := manager.ApplyConfigs(reloadedConfigs); err != nil {
				return errors.Wrap(err, "failed to apply saved config")
			}

			srv.SetBots(manager.DashboardBots())
			srv.SetNeedsSetup(false)
			logger.Info("Applied config from web wizard", zap.String("config", configPath))

			return nil
		}

		cfgPath := ""
		if !configMissing {
			if f := flag.Lookup("config"); f != nil {
				cfgPath = f.Value.String()
			}
		}

		srv = dashboard.NewServer(
			webAddr,
			snapshotStore,
			decisionStore,
			manager.DashboardBots(),
			configMissing,
			cfgPath,
			onSetupSaved,
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			webLogger := logger.With(zap.String("component", "web"))
			if len(tlsDomains) > 0 {
				webLogger.Info("Starting web UI with automatic TLS",
					zap.String("addr", webAddr),
					zap.String("domains", strings.Join(tlsDomains, ",")),
				)
				if err := srv.StartWithAutoTLS(ctx, tlsDomains, tlsCacheDir); err != nil && ctx.Err() == nil {
					webLogger.Fatal("Failed to start web server with automatic TLS", zap.Error(err))
				}
			} else {
				webLogger.Info("Starting web UI", zap.String("addr", webAddr))
				go openBrowser(webLogger, webAddr)
				if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
					webLogger.Fatal("Failed to start web server", zap.Error(err))
				}
			}
		}()
	}

	<-ctx.Done()
	manager.StopAll()
	wg.Wait()
	manager.Wait()

	logger.Info("All trading bots have shut down gracefully")
}

func openBrowser(logger *zap.Logger, addr string) {
	url := addr
	if strings.HasPrefix(url, ":") {
		url = "http://localhost" + url
	} else if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = fmt.Sprintf("http://%s", strings.TrimPrefix(url, "://"))
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}

	if err := cmd.Start(); err != nil {
		logger.Warn("failed to open browser for setup ui", zap.Error(err), zap.String("url", url))
	}
}
