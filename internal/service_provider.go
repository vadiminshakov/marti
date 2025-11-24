package internal

import (
	"context"
	"fmt"
	"sync"

	binance "github.com/adshao/go-binance/v2"
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/vadiminshakov/marti/internal/clients"
	entity "github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/internal/services/market/collector"
	"github.com/vadiminshakov/marti/internal/services/pricer"
	"github.com/vadiminshakov/marti/internal/services/trader"
)

type traderService interface {
	ExecuteAction(ctx context.Context, action entity.Action, amount decimal.Decimal, clientOrderID string) error
	OrderExecuted(ctx context.Context, clientOrderID string) (bool, decimal.Decimal, error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
	GetPosition(ctx context.Context, pair entity.Pair) (*entity.Position, error)
	SetPositionStops(ctx context.Context, pair entity.Pair, takeProfit, stopLoss decimal.Decimal) error
}

type priceService interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

type klineService interface {
	GetKlines(ctx context.Context, pair entity.Pair, interval string, limit int) ([]entity.MarketCandle, error)
}

// serviceProvider defines a factory interface for creating platform-specific services.
type serviceProvider interface {
	Trader(pair entity.Pair, marketType entity.MarketType, leverage int, stateKey string) (traderService, error)
	Pricer() (priceService, error)
	KlineProvider() (klineService, error)
}

// newServiceProvider creates a new service provider based on the client type.
// This is the single point of truth for dispatching to platform-specific implementations.
func newServiceProvider(client any, logger *zap.Logger) (serviceProvider, error) {
	switch c := client.(type) {
	case *binance.Client:
		return &binanceProvider{client: c}, nil
	case *bybit.Client:
		return &bybitProvider{client: c}, nil
	case *clients.SimulateClient:
		return &simulateProvider{client: c, logger: logger}, nil
	case *clients.HyperliquidClient:
		return &hyperliquidProvider{client: c}, nil
	default:
		return nil, fmt.Errorf("unsupported client type: %T", client)
	}
}

type binanceProvider struct {
	client *binance.Client
}

func (p *binanceProvider) Trader(pair entity.Pair, marketType entity.MarketType, leverage int, _ string) (traderService, error) {
	return trader.NewBinanceTrader(p.client, pair, marketType, leverage)
}
func (p *binanceProvider) Pricer() (priceService, error) {
	return pricer.NewBinancePricer(p.client), nil
}
func (p *binanceProvider) KlineProvider() (klineService, error) {
	return collector.NewBinanceKlineProvider(p.client), nil
}

type bybitProvider struct {
	client *bybit.Client
}

func (p *bybitProvider) Trader(pair entity.Pair, marketType entity.MarketType, leverage int, _ string) (traderService, error) {
	return trader.NewBybitTrader(p.client, pair, marketType, leverage)
}
func (p *bybitProvider) Pricer() (priceService, error) {
	return pricer.NewBybitPricer(p.client), nil
}
func (p *bybitProvider) KlineProvider() (klineService, error) {
	return collector.NewBybitKlineProvider(p.client), nil
}

type simulateProvider struct {
	client     *clients.SimulateClient
	logger     *zap.Logger
	pricer     priceService
	pricerOnce sync.Once
}

func (p *simulateProvider) getPricer() priceService {
	p.pricerOnce.Do(func() {
		p.pricer = pricer.NewSimulatePricer(p.client.GetBinanceClient())
	})
	return p.pricer
}

func (p *simulateProvider) Trader(pair entity.Pair, marketType entity.MarketType, leverage int, stateKey string) (traderService, error) {
	return trader.NewSimulateTrader(pair, marketType, leverage, p.logger, p.getPricer(), stateKey)
}
func (p *simulateProvider) Pricer() (priceService, error) {
	return p.getPricer(), nil
}
func (p *simulateProvider) KlineProvider() (klineService, error) {
	return collector.NewBinanceKlineProvider(p.client.GetBinanceClient()), nil
}

type hyperliquidProvider struct {
	client *clients.HyperliquidClient
}

func (p *hyperliquidProvider) Trader(pair entity.Pair, marketType entity.MarketType, leverage int, _ string) (traderService, error) {
	return trader.NewHyperliquidTrader(p.client.Exchange(), p.client.AccountAddress(), pair, marketType, leverage)
}
func (p *hyperliquidProvider) Pricer() (priceService, error) {
	return pricer.NewHyperliquidPricer(p.client.Exchange().Info()), nil
}
func (p *hyperliquidProvider) KlineProvider() (klineService, error) {
	return collector.NewHyperliquidKlineProvider(p.client.Exchange().Info()), nil
}
