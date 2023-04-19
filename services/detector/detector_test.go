package detector

import (
	"github.com/stretchr/testify/require"
	"github.com/vadimInshakov/marti/entity"
	"math/big"
	"testing"
)

func TestNeedAction(t *testing.T) {
	pair := entity.Pair{
		From: "BTC",
		To:   "USDT",
	}
	buypoint := big.NewFloat(100)
	window := big.NewFloat(6)

	d := Detector{
		pair:       pair,
		buypoint:   buypoint,
		window:     window,
		lastAction: entity.ActionBuy,
	}

	act, err := d.NeedAction(big.NewFloat(100))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(101))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(102))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(103))
	require.NoError(t, err)
	require.Equal(t, entity.ActionSell, act)

	// после продажи ждем понижения курса
	act, err = d.NeedAction(big.NewFloat(104))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(101))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(100))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(98))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	// покупаем снова
	act, err = d.NeedAction(big.NewFloat(97))
	require.NoError(t, err)
	require.Equal(t, entity.ActionBuy, act)

	act, err = d.NeedAction(big.NewFloat(102))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	// продажа
	act, err = d.NeedAction(big.NewFloat(103))
	require.NoError(t, err)
	require.Equal(t, entity.ActionSell, act)

	act, err = d.NeedAction(big.NewFloat(99))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(98))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	// покупка
	act, err = d.NeedAction(big.NewFloat(97))
	require.NoError(t, err)
	require.Equal(t, entity.ActionBuy, act)

	// больше не покупаем до продажи
	act, err = d.NeedAction(big.NewFloat(96))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(95))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(94))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)

	act, err = d.NeedAction(big.NewFloat(93))
	require.NoError(t, err)
	require.Equal(t, entity.ActionNull, act)
}
