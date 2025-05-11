package detector

import (
	"fmt"
	"log"

	"github.com/hirokisan/bybit/v2"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/app/entity"
)

type BybitDetector struct {
	pair       entity.Pair
	buypoint   decimal.Decimal
	channel    decimal.Decimal
	lastAction entity.Action
}

func NewBybitDetector(client *bybit.Client, usebalance decimal.Decimal, pair entity.Pair, buypoint, channel decimal.Decimal) (*BybitDetector, error) {
	// Get account balance
	res, err := client.V5().Account().GetWalletBalance(bybit.AccountTypeV5("UNIFIED"), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get account balance: %w", err)
	}

	var fromBalance decimal.Decimal
	var toBalance decimal.Decimal
	for _, coin := range res.Result.List[0].Coin {
		if string(coin.Coin) == pair.To {
			toBalance, _ = decimal.NewFromString(coin.WalletBalance)
		}
		if string(coin.Coin) == pair.From {
			fromBalance, _ = decimal.NewFromString(coin.WalletBalance)
		}
	}

	d := &BybitDetector{pair: pair, buypoint: buypoint, channel: channel}

	// Get current price
	symbol := bybit.SymbolV5(pair.Symbol())
	priceRes, err := client.V5().Market().GetTickers(bybit.V5GetTickersParam{
		Category: "spot",
		Symbol:   &symbol,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get price: %w", err)
	}

	if len(priceRes.Result.Spot.List) == 0 {
		return nil, fmt.Errorf("no price data received for %s", pair.String())
	}

	price, _ := decimal.NewFromString(priceRes.Result.Spot.List[0].LastPrice)

	percent := usebalance.Div(decimal.NewFromInt(100))
	toBalance = toBalance.Mul(percent)

	fromBalanceInSecondCoinsForm := fromBalance.Mul(price)
	if fromBalanceInSecondCoinsForm.Cmp(toBalance) < 0 {
		d.lastAction = entity.ActionSell
	} else {
		d.lastAction = entity.ActionBuy
	}

	log.Printf("last action for pair %s: %s\n", d.pair.String(), d.lastAction.String())

	return d, nil
}

func (d *BybitDetector) NeedAction(price decimal.Decimal) (entity.Action, error) {
	lastact, err := Detect(d.lastAction, d.buypoint, d.channel, price)
	if err != nil {
		return entity.ActionNull, err
	}
	if lastact != entity.ActionNull {
		d.lastAction = lastact
	}

	return lastact, nil
}

func (d *BybitDetector) LastAction() entity.Action {
	return d.lastAction
}
