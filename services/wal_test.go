package services

import (
	"fmt"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestWrappedWal_WriteAndRead(t *testing.T) {
	// Создаем новый WAL
	w, err := NewWrappedWal()
	require.NoError(t, err, "Failed to create WrappedWal")
	defer func() {
		assert.NoError(t, w.Close(), "Failed to close WAL")
	}()

	price := decimal.NewFromFloat(123.45)
	amount := decimal.NewFromFloat(678.90)

	err = w.Write("lastbuy", price)
	require.NoError(t, err, "Failed to write lastbuy")
	err = w.Write("lastamount", amount)
	require.NoError(t, err, "Failed to write lastamount")

	meta, err := w.GetLastBuyMeta()
	require.NoError(t, err, "Failed to get last buy meta")

	assert.True(t, price.Equal(meta.price), "Last buy price mismatch")
	assert.True(t, amount.Equal(meta.amount), "Last buy amount mismatch")

	os.RemoveAll("waldata")
}

func TestWrappedWal_EmptyLog(t *testing.T) {
	w, err := NewWrappedWal()
	require.NoError(t, err, "Failed to create WrappedWal")
	defer func() {
		assert.NoError(t, w.Close(), "Failed to close WAL")
	}()

	_, err = w.GetLastBuyMeta()
	assert.ErrorIs(t, err, ErrNoData, "Expected ErrNoData for empty WAL")

	os.RemoveAll("waldata")
}

func TestWrappedWal_Iterator(t *testing.T) {
	w, err := NewWrappedWal()
	require.NoError(t, err, "Failed to create WrappedWal")
	defer func() {
		assert.NoError(t, w.Close(), "Failed to close WAL")
	}()

	err = w.Write("key1", decimal.NewFromFloat(111.11))
	require.NoError(t, err, "Failed to write key1")
	err = w.Write("key2", decimal.NewFromFloat(222.22))
	require.NoError(t, err, "Failed to write key2")
	err = w.Write("key3", decimal.NewFromFloat(333.33))
	require.NoError(t, err, "Failed to write key3")

	keys := make(map[string]decimal.Decimal)
	for m := range w.wal.Iterator() {
		var value decimal.Decimal
		err := value.UnmarshalBinary(m.Value)
		require.NoError(t, err, fmt.Sprintf("Failed to unmarshal value for key %s", m.Key))
		keys[m.Key] = value
	}

	assert.Equal(t, 3, len(keys), "Unexpected number of keys in WAL")
	assert.True(t, decimal.NewFromFloat(111.11).Equal(keys["key1"]), "Key1 value mismatch")
	assert.True(t, decimal.NewFromFloat(222.22).Equal(keys["key2"]), "Key2 value mismatch")
	assert.True(t, decimal.NewFromFloat(333.33).Equal(keys["key3"]), "Key3 value mismatch")

	os.RemoveAll("waldata")
}

func TestWrappedWal_CorruptedData(t *testing.T) {
	w, err := NewWrappedWal()
	require.NoError(t, err, "Failed to create WrappedWal")

	err = w.Write("lastbuy", decimal.NewFromFloat(100.50))
	require.NoError(t, err, "Failed to write lastbuy")

	w.Close()

	fd, err := os.OpenFile("waldata/seg_0", os.O_RDWR, 0644)
	require.NoError(t, err, "Failed to open WAL segment file")

	_, err = fd.WriteAt([]byte("corrupted data"), 0)
	require.NoError(t, err, "Failed to write corrupted data")

	fd.Close()

	w, err = NewWrappedWal()
	require.Error(t, err, "Expected an error due to corrupted data")

	os.RemoveAll("waldata")
}

func TestWalReload(t *testing.T) {
	// check that after WAL reload data is saved
	w, err := NewWrappedWal()
	require.NoError(t, err, "Не удалось создать WAL")

	price := decimal.NewFromFloat(1234.5678)
	amount := decimal.NewFromFloat(0.1234)

	// write data
	err = w.Write("lastbuy", price)
	require.NoError(t, err, "Ошибка записи цены в WAL")
	err = w.Write("lastamount", amount)
	require.NoError(t, err, "Ошибка записи количества в WAL")

	// close WAL
	err = w.Close()
	require.NoError(t, err, "Ошибка закрытия WAL")

	// reload WAL
	w, err = NewWrappedWal()
	require.NoError(t, err, "Ошибка пересоздания WAL")

	// write data
	err = w.Write("lastbuy", price)
	require.NoError(t, err, "Ошибка записи цены в WAL")
	err = w.Write("lastamount", amount)
	require.NoError(t, err, "Ошибка записи количества в WAL")
	err = w.Write("lastamount2", amount)
	require.NoError(t, err, "Ошибка записи количества в WAL")

	// close WAL
	err = w.Close()
	require.NoError(t, err, "Ошибка закрытия WAL")

	// reload WAL
	w, err = NewWrappedWal()
	require.NoError(t, err, "Ошибка пересоздания WAL")

	err = w.Write("1lastbuy", price)
	require.NoError(t, err, "Ошибка записи цены в WAL")

	// close WAL
	err = w.Close()
	require.NoError(t, err, "Ошибка закрытия WAL")

	// reload WAL
	w, err = NewWrappedWal()
	require.NoError(t, err, "Ошибка пересоздания WAL")

	os.RemoveAll("waldata")
}
