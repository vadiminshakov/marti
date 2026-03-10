package main

import (
	"context"
	"os"
	"sync"

	"github.com/pkg/errors"
	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/dashboard"
	"github.com/vadiminshakov/marti/internal"
	"github.com/vadiminshakov/marti/internal/clients"
	"github.com/vadiminshakov/marti/internal/storage/balancesnapshots"
	"github.com/vadiminshakov/marti/internal/storage/decisions"
	"go.uber.org/zap"
)

type managedBot struct {
	ctx    context.Context
	bot    *internal.TradingBot
	cancel context.CancelFunc
}

type botManager struct {
	ctx           context.Context
	logger        *zap.Logger
	snapshotStore *balancesnapshots.WALStore
	decisionStore *decisions.WALStore

	mu   sync.RWMutex
	bots []managedBot
	wg   sync.WaitGroup
}

func newBotManager(
	ctx context.Context,
	logger *zap.Logger,
	snapshotStore *balancesnapshots.WALStore,
	decisionStore *decisions.WALStore,
) *botManager {
	return &botManager{
		ctx:           ctx,
		logger:        logger,
		snapshotStore: snapshotStore,
		decisionStore: decisionStore,
	}
}

func (m *botManager) ApplyConfigs(configs []config.Config) error {
	newBots := make([]managedBot, 0, len(configs))

	for _, cfg := range configs {
		botLogger := m.logger.With(
			zap.String("platform", cfg.Platform),
			zap.String("pair", cfg.Pair.String()),
		)

		client, err := newExchangeClient(cfg, botLogger)
		if err != nil {
			return errors.Wrap(err, "failed to initialize exchange client")
		}

		bot, err := internal.NewTradingBot(botLogger, cfg, client, m.snapshotStore, m.decisionStore)
		if err != nil {
			return errors.Wrap(err, "failed to create trading bot")
		}

		runCtx, runCancel := context.WithCancel(m.ctx)
		newBots = append(newBots, managedBot{
			ctx:    runCtx,
			bot:    bot,
			cancel: runCancel,
		})
	}

	m.mu.Lock()
	oldBots := m.bots
	m.bots = newBots
	m.mu.Unlock()

	for _, managed := range newBots {
		botLogger := m.logger.With(
			zap.String("platform", managed.bot.Config.Platform),
			zap.String("pair", managed.bot.Config.Pair.String()),
		)

		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer managed.bot.Close()
			if err := managed.bot.Run(managed.ctx, botLogger); err != nil && managed.ctx.Err() == nil {
				botLogger.Error("Trading bot failed", zap.Error(err))
			}
		}()
	}

	for _, managed := range oldBots {
		managed.cancel()
	}

	return nil
}

func (m *botManager) DashboardBots() []dashboard.TradingBotInterface {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bots := make([]dashboard.TradingBotInterface, 0, len(m.bots))
	for _, managed := range m.bots {
		bots = append(bots, managed.bot)
	}

	return bots
}

func (m *botManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, managed := range m.bots {
		managed.cancel()
	}
}

func (m *botManager) Wait() {
	m.wg.Wait()
}

func newExchangeClient(cfg config.Config, logger *zap.Logger) (any, error) {
	switch cfg.Platform {
	case "binance":
		apiKey := os.Getenv("BINANCE_API_KEY")
		apiSecret := os.Getenv("BINANCE_API_SECRET")
		if apiKey == "" || apiSecret == "" {
			return nil, errors.New("missing Binance credentials")
		}

		return clients.NewBinanceClient(apiKey, apiSecret), nil
	case "bybit":
		apiKey := os.Getenv("BYBIT_API_KEY")
		apiSecret := os.Getenv("BYBIT_API_SECRET")
		if apiKey == "" || apiSecret == "" {
			return nil, errors.New("missing Bybit credentials")
		}

		return clients.NewBybitClient(apiKey, apiSecret), nil
	case "simulate":
		logger.Info("Using simulation mode - no real trades will be executed",
			zap.String("pair", cfg.Pair.String()))

		return clients.NewSimulateClient(), nil
	case "hyperliquid":
		pk := os.Getenv("HYPERLIQ_PRIVATE_KEY")
		if pk == "" {
			return nil, errors.New("missing Hyperliquid private key")
		}

		baseURL := os.Getenv("HYPERLIQ_API_URL") // optional; defaults to mainnet
		hl, err := clients.NewHyperliquidClient(pk, baseURL)
		if err != nil {
			return nil, errors.Wrap(err, "failed to init Hyperliquid client")
		}

		return hl, nil
	default:
		return nil, errors.Errorf("unsupported platform: %s", cfg.Platform)
	}
}
