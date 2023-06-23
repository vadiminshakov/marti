package main

import (
	"encoding/csv"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services"
	"github.com/vadimInshakov/marti/services/anomalydetector"
	"github.com/vadimInshakov/marti/services/detector"
	"github.com/vadimInshakov/marti/services/windowfinder"
	"log"
	"os"
	"testing"
	"time"
)

type pricerCsv struct {
	pricesCh chan decimal.Decimal
}

func (p *pricerCsv) GetPrice(pair entity.Pair) (decimal.Decimal, error) {
	return <-p.pricesCh, nil
}

type detectorCsv struct {
	lastaction entity.Action
	buypoint   decimal.Decimal
	window     decimal.Decimal
}

func (d *detectorCsv) NeedAction(price decimal.Decimal) (entity.Action, error) {
	lastact, err := detector.Detect(d.lastaction, d.buypoint, d.window, price)
	if err != nil {
		return entity.ActionNull, err
	}
	if d.lastaction != entity.ActionNull {
		d.lastaction = lastact
	}

	return lastact, nil
}

func (d *detectorCsv) LastAction() entity.Action {
	return d.lastaction
}

type traderCsv struct {
	pair     *entity.Pair
	balance1 decimal.Decimal
	balance2 decimal.Decimal
	pricesCh chan decimal.Decimal
}

// Buy buys amount of asset in trade pair.
func (t *traderCsv) Buy(amount decimal.Decimal) error {
	t.balance1 = t.balance1.Add(amount)
	price := <-t.pricesCh
	t.balance2 = t.balance2.Sub(price.Mul(amount))

	return nil
}

// Sell sells amount of asset in trade pair.
func (t *traderCsv) Sell(amount decimal.Decimal) error {
	t.balance1 = t.balance1.Sub(amount)
	price := <-t.pricesCh
	t.balance2 = t.balance2.Add(price.Mul(amount))
	return nil
}

func Test1Year(t *testing.T) {
	pair := &entity.Pair{
		From: "BTC",
		To:   "USDT",
	}

	prices, klines := makePriceChFromCsv("data/data.csv")
	pricer := &pricerCsv{
		pricesCh: prices,
	}

	balanceBTC, _ := decimal.NewFromString("0.5")
	balanceUSDT := decimal.NewFromInt(0)
	trader := &traderCsv{
		pair:     pair,
		balance1: balanceBTC,
		balance2: balanceUSDT,
		pricesCh: prices,
	}

	anomdetector := anomalydetector.NewAnomalyDetector(*pair, 30, decimal.NewFromInt(10))

	var lastaction entity.Action = entity.ActionBuy

	kline := <-klines
	buyprice, window, _ := windowfinder.CalcBuyPriceAndWindow([]*entity.Kline{&kline}, decimal.NewFromInt(100))

	ts := services.NewTradeService(*pair, balanceBTC, pricer, &detectorCsv{
		lastaction: lastaction,
		buypoint:   buyprice,
		window:     window,
	},
		trader, anomdetector)

	var counter uint
	var kl []*entity.Kline
	var klinesframe uint = 4
	for {
		counter++
		if len(prices) == 0 || len(klines) == 0 {
			break
		}

		kline := <-klines
		if len(kl) >= int(klinesframe) {
			kl = kl[1:]
		}
		kl = append(kl, &kline)

		if counter == klinesframe {
			counter = 0
			// recreate trade service for each day
			buyprice, window, _ = windowfinder.CalcBuyPriceAndWindow(kl, decimal.NewFromInt(100))

			ts = services.NewTradeService(*pair, balanceBTC, pricer, &detectorCsv{
				lastaction: lastaction,
				buypoint:   buyprice,
				window:     window,
			},
				trader, anomdetector)
		}

		te, err := ts.Trade()
		require.NoError(t, err)

		if te == nil {
			<-prices // skip price that not readed by trader
			continue
		}
		if te.Action != entity.ActionNull {
			lastaction = te.Action
		}

		log.Println(te.String())
	}

	log.Printf("Total balance of %s is %s (was %s)", pair.From, trader.balance1.String(), balanceBTC.String())
	log.Printf("Total balance of %s is %s (was %s)", pair.To, trader.balance2.String(), balanceUSDT.String())
}

func makePriceChFromCsv(filePath string) (chan decimal.Decimal, chan entity.Kline) {
	prices := make(chan decimal.Decimal, 1000)
	var klines chan entity.Kline

	go func() {
		pricescsv, kchan := readCsv(filePath)
		klines = kchan
		for _, price := range pricescsv {
			prices <- price // for pricer
			prices <- price // for trader
		}
	}()
	time.Sleep(2000 * time.Millisecond)

	return prices, klines
}

func readCsv(filePath string) ([]decimal.Decimal, chan entity.Kline) {
	f, err := os.Open(filePath)
	if err != nil {
		log.Fatal("Unable to read input file "+filePath, err)
	}
	defer f.Close()

	csvReader := csv.NewReader(f)
	records, err := csvReader.ReadAll()
	if err != nil {
		log.Fatal("Unable to parse file as CSV for "+filePath, err)
	}

	prices := make([]decimal.Decimal, 0, len(records))
	klines := make(chan entity.Kline, len(records))

	for _, record := range records {
		priceOpen, _ := decimal.NewFromString(record[0])
		priceHigh, _ := decimal.NewFromString(record[1])
		priceLow, _ := decimal.NewFromString(record[2])
		priceClose, _ := decimal.NewFromString(record[3])

		price := priceHigh.Add(priceLow).Div(decimal.NewFromInt(2))
		prices = append(prices, price)
		klines <- entity.Kline{
			Open:  priceOpen,
			Close: priceClose,
		}
	}

	return prices, klines
}
