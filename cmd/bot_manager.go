package main

import (
	"context"
	"os"
	"sync"

	"github.com/pkg/errors"
	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/dashboard"
	"github.com/vadiminshakov/marti/internal/clients"
	domain "github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/internal/repository/balancesnapshots"
	botsvc "github.com/vadiminshakov/marti/internal/services/bot"
	"go.uber.org/zap"
)

type decisionStoreWriter interface {
	SaveAI(event domain.AIDecisionEvent) error
	SaveDCA(event domain.DCADecisionEvent) error
}

type managedBot struct {
	ctx    context.Context
	bot    *botsvc.TradingBot
	cancel context.CancelFunc
	done   chan struct{}
}

type botManager struct {
	ctx           context.Context
	logger        *zap.Logger
	snapshotStore *balancesnapshots.WALStore
	decisionStore decisionStoreWriter

	mu   sync.RWMutex
	bots []managedBot
	wg   sync.WaitGroup
}

func newBotManager(
	ctx context.Context,
	logger *zap.Logger,
	snapshotStore *balancesnapshots.WALStore,
	decisionStore decisionStoreWriter,
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

		tradingBot, err := botsvc.NewTradingBot(botLogger, cfg, client, m.snapshotStore, m.decisionStore)
		if err != nil {
			return errors.Wrap(err, "failed to create trading bot")
		}

		runCtx, runCancel := context.WithCancel(m.ctx)
		newBots = append(newBots, managedBot{
			ctx:    runCtx,
			bot:    tradingBot,
			cancel: runCancel,
			done:   make(chan struct{}),
		})
	}

	m.mu.Lock()
	oldBots := m.bots
	m.bots = nil
	m.mu.Unlock()

	for _, managed := range oldBots {
		managed.cancel()
	}
	m.waitForManagedBots(oldBots)

	m.mu.Lock()
	m.bots = newBots
	m.mu.Unlock()

	for _, managed := range newBots {
		m.startManagedBot(managed)
	}

	return nil
}

func (m *botManager) startManagedBot(managed managedBot) {
	botLogger := m.logger.With(
		zap.String("platform", managed.bot.Config.Platform),
		zap.String("pair", managed.bot.Config.Pair.String()),
	)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(managed.done)
		defer managed.bot.Close()
		if err := managed.bot.Run(managed.ctx, botLogger); err != nil && managed.ctx.Err() == nil {
			botLogger.Error("Trading bot failed", zap.Error(err))
		}
	}()
}

func (m *botManager) waitForManagedBots(bots []managedBot) {
	for _, managed := range bots {
		if managed.done == nil {
			continue
		}
		<-managed.done
	}
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
