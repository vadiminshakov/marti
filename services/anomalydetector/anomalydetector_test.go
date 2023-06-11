package anomalydetector

import (
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/vadimInshakov/marti/entity"
	"testing"
)

func TestAnomalyDetector(t *testing.T) {
	anomalyd := NewAnomalyDetector(entity.Pair{"BTC", "USD"}, 5, decimal.NewFromInt(10))

	// fill buffer
	require.False(t, anomalyd.IsAnomaly(decimal.NewFromInt(100)))
	require.False(t, anomalyd.IsAnomaly(decimal.NewFromInt(101)))
	require.False(t, anomalyd.IsAnomaly(decimal.NewFromInt(102)))
	require.False(t, anomalyd.IsAnomaly(decimal.NewFromInt(103)))
	require.False(t, anomalyd.IsAnomaly(decimal.NewFromInt(104)))

	// not anomaly
	require.False(t, anomalyd.IsAnomaly(decimal.NewFromInt(105)))
	require.False(t, anomalyd.IsAnomaly(decimal.NewFromInt(106)))

	// anomaly
	require.True(t, anomalyd.IsAnomaly(decimal.NewFromInt(200)))
}
