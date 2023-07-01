package services

import (
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vadimInshakov/marti/entity"
	anomalymock "github.com/vadimInshakov/marti/services/anomalydetector/mock"
	detectormock "github.com/vadimInshakov/marti/services/detector/mock"
	tradermock "github.com/vadimInshakov/marti/services/trader/mock"
	"go.uber.org/zap"
	"testing"
)

type pricemock struct {
	n int64
}

func (p *pricemock) GetPrice(_ entity.Pair) (decimal.Decimal, error) {
	p.n += 1
	return decimal.NewFromInt(p.n), nil
}

func TestTrade(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USD"}

	pricer := &pricemock{}

	trader := tradermock.NewTrader(t)
	trader.On("Buy", mock.Anything).Return(nil)
	trader.On("Sell", mock.Anything).Return(nil)

	detector := detectormock.NewDetector(t)
	detector.On("NeedAction", decimal.NewFromInt(1)).Return(entity.ActionBuy, nil)
	detector.On("NeedAction", decimal.NewFromInt(3)).Return(entity.ActionSell, nil)
	detector.On("NeedAction", decimal.NewFromInt(2)).Return(entity.ActionNull, nil)
	detector.On("NeedAction", decimal.NewFromInt(4)).Return(entity.ActionNull, nil)
	detector.On("NeedAction", decimal.NewFromInt(5)).Return(entity.ActionNull, nil)

	anomalyDetector := anomalymock.NewAnomalyDetector(t)
	anomalyDetector.On("IsAnomaly", mock.Anything).Return(false, nil)

	amount := decimal.NewFromInt(1)

	l, err := zap.NewProduction()
	assert.NoError(t, err)
	ts := NewTradeService(l, pair, amount, pricer, detector, trader, anomalyDetector)
	event, err := ts.Trade()
	assert.NoError(t, err)
	assert.Equal(t, entity.ActionBuy, event.Action)

	event, err = ts.Trade()
	assert.NoError(t, err)
	assert.Nil(t, event)

	event, err = ts.Trade()
	assert.NoError(t, err)
	assert.Equal(t, entity.ActionSell, event.Action)

	event, err = ts.Trade()
	assert.NoError(t, err)
	assert.Nil(t, event)

	event, err = ts.Trade()
	assert.NoError(t, err)
	assert.Nil(t, event)

	trader.AssertNumberOfCalls(t, "Buy", 1)
	trader.AssertNumberOfCalls(t, "Sell", 1)
}
