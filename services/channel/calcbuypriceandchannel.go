package channel

import (
	"fmt"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/entity"
)

func CalcBuyPriceAndChannel[T entity.Kliner](klines []T, minwindow decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	cumulativeBuyPrice, cumulativeChannel := decimal.NewFromInt(0), decimal.NewFromInt(0)

	for _, k := range klines {
		klinesum := k.OpenPrice().Add(k.ClosePrice())
		buyprice := klinesum.Div(decimal.NewFromInt(2))
		cumulativeBuyPrice = cumulativeBuyPrice.Add(buyprice)
		klinewindow := k.OpenPrice().Sub(k.ClosePrice()).Abs()
		cumulativeChannel = cumulativeChannel.Add(klinewindow)
	}

	cumulativeBuyPrice = cumulativeBuyPrice.Div(decimal.NewFromInt(int64(len(klines))))
	cumulativeChannel = cumulativeChannel.Div(decimal.NewFromInt(int64(len(klines))))

	if cumulativeChannel.Cmp(minwindow) < 0 {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("channel less then min (found %s, min %s)", cumulativeChannel.String(), minwindow.String())
	}

	return cumulativeBuyPrice, cumulativeChannel, nil
}
