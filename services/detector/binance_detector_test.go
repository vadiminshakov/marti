package detector

import (
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/vadimInshakov/marti/entity"
	"testing"
)

func TestNeedAction(t *testing.T) {
	pair := entity.Pair{
		From: "BTC",
		To:   "USDT",
	}
	buypoint := decimal.NewFromInt(100)
	window := decimal.NewFromInt(6)

	d := Detector{
		pair:       pair,
		buypoint:   buypoint,
		window:     window,
		lastAction: entity.ActionBuy,
	}

	act, err := d.NeedAction(decimal.NewFromInt(100))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(101))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(102))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(103))
	require.NoError(t, err)
	require.Equal(t, entity.ActionSell, act)

	// after sell wait price down
	act, err = d.NeedAction(decimal.NewFromInt(104))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(101))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(100))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(98))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	// buy again
	act, err = d.NeedAction(decimal.NewFromInt(97))
	require.NoError(t, err)
	require.Equal(t, entity.ActionBuy, act)

	act, err = d.NeedAction(decimal.NewFromInt(102))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	// sell
	act, err = d.NeedAction(decimal.NewFromInt(103))
	require.NoError(t, err)
	require.Equal(t, entity.ActionSell, act)

	act, err = d.NeedAction(decimal.NewFromInt(99))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(98))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	// buy
	act, err = d.NeedAction(decimal.NewFromInt(97))
	require.NoError(t, err)
	require.Equal(t, entity.ActionBuy, act)

	// do not buy until sell
	act, err = d.NeedAction(decimal.NewFromInt(96))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(95))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(94))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(decimal.NewFromInt(93))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)
}
