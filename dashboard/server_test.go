package dashboard

import (
	"testing"

	"github.com/vadiminshakov/marti/internal/domain"
)

func makeRecords(n int) []domain.BalanceSnapshotRecord {
	records := make([]domain.BalanceSnapshotRecord, n)
	for i := 0; i < n; i++ {
		records[i] = domain.BalanceSnapshotRecord{
			Index:    uint64(i),
			Snapshot: domain.BalanceSnapshot{Pair: "BTCUSDT"},
		}
	}
	return records
}

func TestThinRecordsSmall(t *testing.T) {
	records := makeRecords(2000)
	result := thinRecords(records)
	if len(result) != 2000 {
		t.Fatalf("expected 2000 records (below keepLast), got %d", len(result))
	}
}

func TestThinRecordsLast2500Preserved(t *testing.T) {
	records := makeRecords(5000)
	result := thinRecords(records)
	if len(result) >= 5000 {
		t.Fatalf("expected fewer records, got %d", len(result))
	}

	// last 2500 must be present and in order.
	tail := result[len(result)-2500:]
	for i, rec := range tail {
		expected := uint64(2500 + i)
		if rec.Index != expected {
			t.Fatalf("tail record %d: expected index %d, got %d", i, expected, rec.Index)
		}
	}
}

func TestThinRecordsExponential(t *testing.T) {
	records := makeRecords(50000)
	result := thinRecords(records)

	// with exponential thinning, 50000 records should reduce well below linear.
	if len(result) > 5000 {
		t.Fatalf("expected exponential thinning to produce <5000 records for 50000 input, got %d", len(result))
	}
	if len(result) < 2550 {
		t.Fatalf("expected at least 2550 records (2500 tail + thinned), got %d", len(result))
	}
}

func TestThinRecordsGapsGrow(t *testing.T) {
	records := makeRecords(10000)
	result := thinRecords(records)

	// verify gaps between consecutive thinned records grow (exponential property).
	// examine the thinned portion (exclude the last 2500 which are dense).
	thinnedPart := result[:len(result)-2500]
	if len(thinnedPart) < 30 {
		t.Fatalf("thinned part too small: %d", len(thinnedPart))
	}

	// gaps should increase as we go further back in time (earlier in the slice).
	largeGapFound := false
	for i := 1; i < len(thinnedPart); i++ {
		gap := thinnedPart[i].Index - thinnedPart[i-1].Index
		if gap > 10 {
			largeGapFound = true
			break
		}
	}
	if !largeGapFound {
		t.Fatalf("expected gaps to grow exponentially, but no gap > 10 found in thinned portion")
	}
}
