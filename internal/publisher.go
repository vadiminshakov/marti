package internal

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	entity "github.com/vadiminshakov/marti/internal/domain"
)

func (b *TradingBot) streamBalances(ctx context.Context, logger *zap.Logger) {
	interval := b.Config.PollPriceInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if err := b.publishBalanceSnapshot(ctx); err != nil {
		logger.Debug("balance snapshot skipped", zap.Error(err))
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.publishBalanceSnapshot(ctx); err != nil {
				logger.Debug("balance snapshot skipped", zap.Error(err))
			}
		}
	}
}

func (b *TradingBot) publishBalanceSnapshot(ctx context.Context) error {
	base, err := b.trader.GetBalance(ctx, b.Config.Pair.From)
	if err != nil {
		return errors.Wrapf(err, "get %s balance", b.Config.Pair.From)
	}

	quote, err := b.trader.GetBalance(ctx, b.Config.Pair.To)
	if err != nil {
		return errors.Wrapf(err, "get %s balance", b.Config.Pair.To)
	}

	price, err := b.pricer.GetPrice(ctx, b.Config.Pair)
	if err != nil {
		return errors.Wrap(err, "get price for balance snapshot")
	}

	total := quote.Add(base.Mul(price))

	var (
		activePosition                            string
		entryPrice, positionAmount, unrealizedPnL string
	)

	if b.Config.MarketType == entity.MarketTypeMargin {
		position, posErr := b.trader.GetPosition(ctx, b.Config.Pair)
		if posErr != nil {
			return errors.Wrap(posErr, "get position for balance snapshot")
		}

		if position != nil && position.Amount.GreaterThan(decimal.Zero) {
			switch position.Side {
			case entity.PositionSideLong:
				activePosition = "long"
			case entity.PositionSideShort:
				activePosition = "short"
			}

			total = position.CalculateTotalEquity(price, base, quote, b.leverage)
			entryPrice = position.EntryPrice.String()
			positionAmount = position.Amount.String()
			unrealizedPnL = position.PnL(price).StringFixed(2)
		}
	}

	if b.Config.MarketType == entity.MarketTypeSpot {
		if provider, ok := b.tradingStrategy.(DcaCostBasisProvider); ok {
			avgPrice, amt := provider.GetDcaCostBasis()
			if amt.GreaterThan(decimal.Zero) && avgPrice.GreaterThan(decimal.Zero) {
				entryPrice = avgPrice.String()
				positionAmount = amt.String()
				pnl := price.Sub(avgPrice).Mul(amt)
				unrealizedPnL = pnl.StringFixed(2)
				activePosition = "long"
			}
		}
	}

	model := b.Config.Model

	if b.Config.StrategyType == "dca" {
		model = "DCA"
	}

	err = b.balanceStore.Save(entity.NewBalanceSnapshot(
		time.Now().UTC(),
		b.Config.Pair.String(),
		model,
		base.String(),
		quote.String(),
		total.StringFixed(2),
		price.String(),
		activePosition,
		entryPrice,
		positionAmount,
		unrealizedPnL,
	))

	return errors.Wrap(err, "failed to save balance snapshot")
}
