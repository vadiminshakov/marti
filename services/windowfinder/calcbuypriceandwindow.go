package windowfinder

import (
	"fmt"
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
)

func CalcBuyPriceAndWindow[T entity.Kliner](klines []T, minwindow decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	cumulativeBuyPrice, cumulativeWindow := decimal.NewFromInt(0), decimal.NewFromInt(0)

	for _, k := range klines {
		klinesum := k.OpenPrice().Add(k.ClosePrice())
		buyprice := klinesum.Div(decimal.NewFromInt(2))
		cumulativeBuyPrice = cumulativeBuyPrice.Add(buyprice)

		klinewindow := k.OpenPrice().Sub(k.ClosePrice()).Abs()
		cumulativeWindow = cumulativeWindow.Add(klinewindow)
	}

	cumulativeBuyPrice = cumulativeBuyPrice.Div(decimal.NewFromInt(int64(len(klines))))
	cumulativeWindow = cumulativeWindow.Div(decimal.NewFromInt(int64(len(klines))))

	if cumulativeWindow.Cmp(minwindow) < 0 {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("window less then min (found %s, min %s)", cumulativeWindow.String(), minwindow.String())
	}
	return cumulativeBuyPrice, cumulativeWindow, nil
}
