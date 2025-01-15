package channel

import (
	"fmt"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/entity"
)

// The minTradeChannelPercent constant represents the minimum percentage of the trade channel relative
// to the average prices of each kline.
// It ensures that the calculated trade channel is not too narrow. If the calculated trade channel is less than this minimum percentage,
// an error is returned, indicating that the channel is too small for trading.
const minTradeChannelPercent = 0.0015 // 0.15% of the average price

func CalcBuyPriceAndChannel[T entity.Kliner](klines []T) (decimal.Decimal, decimal.Decimal, error) {
	if len(klines) == 0 {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("klines array is empty")
	}

	averageBuyPrice, averageChannelWidth := decimal.NewFromInt(0), decimal.NewFromInt(0)

	for _, k := range klines {
		klineSum := k.OpenPrice().Add(k.ClosePrice())
		buyPrice := klineSum.Div(decimal.NewFromInt(2))
		averageBuyPrice = averageBuyPrice.Add(buyPrice)

		// Calculate channel width (use HighPrice - LowPrice if more precise channel is needed)
		channelWidth := k.OpenPrice().Sub(k.ClosePrice()).Abs()
		averageChannelWidth = averageChannelWidth.Add(channelWidth)
	}

	// Calculate averages
	averageBuyPrice = averageBuyPrice.Div(decimal.NewFromInt(int64(len(klines))))
	averageChannelWidth = averageChannelWidth.Div(decimal.NewFromInt(int64(len(klines))))

	// Minimum channel width check
	minTradeChannel := averageBuyPrice.Mul(decimal.NewFromFloat(minTradeChannelPercent))
	if averageChannelWidth.Cmp(minTradeChannel) < 0 {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf(
			"channel less than min (found %s, min %s)",
			averageChannelWidth.String(),
			minTradeChannel.String(),
		)
	}

	return averageBuyPrice, averageChannelWidth, nil
}
