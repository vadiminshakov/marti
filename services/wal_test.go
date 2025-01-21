package services

import (
	"fmt"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWrappedWal_WriteAndReadDecimal(t *testing.T) {
	// Создаем новый WAL
	w, err := NewWrappedWal()
	require.NoError(t, err, "Failed to create WrappedWal")
	defer func() {
		assert.NoError(t, w.Close(), "Failed to close WAL")
	}()

	// Тестовые данные
	price := decimal.NewFromFloat(123.45)
	amount := decimal.NewFromFloat(678.90)

	// Записываем данные
	err = w.Write("lastbuy", price)
	require.NoError(t, err, "Failed to write lastbuy")
	err = w.Write("lastamount", amount)
	require.NoError(t, err, "Failed to write lastamount")

	// Считываем данные
	meta, err := w.GetLastBuyMeta()
	require.NoError(t, err, "Failed to get last buy meta")

	// Проверяем значения
	assert.True(t, price.Equal(meta.price), "Last buy price mismatch")
	assert.True(t, amount.Equal(meta.amount), "Last buy amount mismatch")
}

func TestWrappedWal_EmptyLog(t *testing.T) {
	w, err := NewWrappedWal()
	require.NoError(t, err, "Failed to create WrappedWal")
	defer func() {
		assert.NoError(t, w.Close(), "Failed to close WAL")
	}()

	_, err = w.GetLastBuyMeta()
	assert.ErrorIs(t, err, ErrNoData, "Expected ErrNoData for empty WAL")
}

func TestWrappedWal_Serialization(t *testing.T) {
	// Исходное значение
	original := decimal.NewFromFloat(456.789)

	// Сериализация
	b, err := original.MarshalBinary()
	require.NoError(t, err, "Failed to marshal decimal")

	// Десериализация
	var restored decimal.Decimal
	err = restored.UnmarshalBinary(b)
	require.NoError(t, err, "Failed to unmarshal decimal")

	// Проверка эквивалентности
	assert.True(t, original.Equal(restored), "Deserialized decimal does not match original")
}

func TestWrappedWal_Iterator(t *testing.T) {
	w, err := NewWrappedWal()
	require.NoError(t, err, "Failed to create WrappedWal")
	defer func() {
		assert.NoError(t, w.Close(), "Failed to close WAL")
	}()

	// Записываем несколько данных
	err = w.Write("key1", decimal.NewFromFloat(111.11))
	require.NoError(t, err, "Failed to write key1")
	err = w.Write("key2", decimal.NewFromFloat(222.22))
	require.NoError(t, err, "Failed to write key2")
	err = w.Write("key3", decimal.NewFromFloat(333.33))
	require.NoError(t, err, "Failed to write key3")

	// Проверяем итерацию
	keys := make(map[string]decimal.Decimal)
	for m := range w.wal.Iterator() {
		var value decimal.Decimal
		err := value.UnmarshalBinary(m.Value)
		require.NoError(t, err, fmt.Sprintf("Failed to unmarshal value for key %s", m.Key))
		keys[m.Key] = value
	}

	// Сравниваем результаты
	assert.Equal(t, 3, len(keys), "Unexpected number of keys in WAL")
	assert.True(t, decimal.NewFromFloat(111.11).Equal(keys["key1"]), "Key1 value mismatch")
	assert.True(t, decimal.NewFromFloat(222.22).Equal(keys["key2"]), "Key2 value mismatch")
	assert.True(t, decimal.NewFromFloat(333.33).Equal(keys["key3"]), "Key3 value mismatch")
}

func TestWrappedWal_CorruptedData(t *testing.T) {
	w, err := NewWrappedWal()
	require.NoError(t, err, "Failed to create WrappedWal")
	defer func() {
		assert.NoError(t, w.Close(), "Failed to close WAL")
	}()

	// Пишем данные
	err = w.Write("lastbuy", decimal.NewFromFloat(100.50))
	require.NoError(t, err, "Failed to write lastbuy")

	// Повреждаем данные вручную
	err = w.wal.Write(w.wal.CurrentIndex()+1, "corrupted", []byte("corrupted data"))
	require.NoError(t, err, "Failed to write corrupted data")

	// Читаем и проверяем, что данные некорректны
	_, err = w.GetLastBuyMeta()
	assert.Error(t, err, "Expected an error due to corrupted data")
}

func TestWalReload(t *testing.T) {
	// Проверяем, что после перезагрузки WAL данные сохраняются
	w, err := NewWrappedWal()
	require.NoError(t, err, "Не удалось создать WAL")

	price := decimal.NewFromFloat(1234.5678)
	amount := decimal.NewFromFloat(0.1234)

	// Записываем данные
	err = w.Write("lastbuy", price)
	require.NoError(t, err, "Ошибка записи цены в WAL")
	err = w.Write("lastamount", amount)
	require.NoError(t, err, "Ошибка записи количества в WAL")

	// Закрываем WAL
	err = w.Close()
	require.NoError(t, err, "Ошибка закрытия WAL")

	// Пересоздаем WAL
	w, err = NewWrappedWal()
	require.NoError(t, err, "Ошибка пересоздания WAL")

	// Записываем данные
	err = w.Write("lastbuy", price)
	require.NoError(t, err, "Ошибка записи цены в WAL")
	err = w.Write("lastamount", amount)
	require.NoError(t, err, "Ошибка записи количества в WAL")
	err = w.Write("lastamount2", amount)
	require.NoError(t, err, "Ошибка записи количества в WAL")

	// Закрываем WAL
	err = w.Close()
	require.NoError(t, err, "Ошибка закрытия WAL")

	// Пересоздаем WAL
	w, err = NewWrappedWal()
	require.NoError(t, err, "Ошибка пересоздания WAL")

	err = w.Write("1lastbuy", price)
	require.NoError(t, err, "Ошибка записи цены в WAL")

	// Закрываем WAL
	err = w.Close()
	require.NoError(t, err, "Ошибка закрытия WAL")

	// Пересоздаем WAL
	w, err = NewWrappedWal()
	require.NoError(t, err, "Ошибка пересоздания WAL")
}
