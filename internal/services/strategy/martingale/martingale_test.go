package martingale_test

import (
	"os"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/internal/services/strategy/martingale"
	recorderMock "github.com/vadiminshakov/marti/mocks/decisionrecorder"
	pricerMock "github.com/vadiminshakov/marti/mocks/pricer"
	traderMock "github.com/vadiminshakov/marti/mocks/trader"
)

func TestMartingaleStrategy_IncreasingStepSizes(t *testing.T) {
	multiplier := decimal.NewFromInt(2)
	allocated := decimal.NewFromInt(1000)
	maxTrades := 4

	sum := decimal.Zero
	for i := range maxTrades {
		sum = sum.Add(multiplier.Pow(decimal.NewFromInt(int64(i))))
	}

	amounts := make([]decimal.Decimal, maxTrades)
	for step := range maxTrades {
		weight := multiplier.Pow(decimal.NewFromInt(int64(step)))
		amounts[step] = allocated.Mul(weight).Div(sum)
	}

	for i := 1; i < maxTrades; i++ {
		require.True(t, amounts[i].GreaterThan(amounts[i-1]),
			"step %d (%s) should be larger than step %d (%s)", i, amounts[i], i-1, amounts[i-1])
	}

	total := decimal.Zero
	for _, a := range amounts {
		total = total.Add(a)
	}
	require.True(t, total.Round(8).Equal(allocated.Round(8)),
		"sum of all steps should equal allocated: got %s", total)
}

func TestMartingaleStrategy_Creation(t *testing.T) {
	dir, err := os.MkdirTemp("", "martingale_create_*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	pair := domain.Pair{From: "BTC", To: "USDT"}
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	recorder := recorderMock.NewDecisionRecorder(t)

	strat, err := martingale.NewMartingaleStrategy(
		zap.NewNop(),
		dir,
		pair,
		decimal.NewFromInt(10),
		mockPricer,
		mockTrader,
		recorder,
		5,
		decimal.NewFromFloat(3.5),
		decimal.NewFromFloat(0.75),
		decimal.NewFromInt(2),
	)
	require.NoError(t, err)
	require.NotNil(t, strat)
	strat.Close()
}
