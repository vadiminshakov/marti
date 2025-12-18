package domain

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

const (
	percentageMultiplier = 100
)

// DCAPurchase represents a single DCA purchase.
type DCAPurchase struct {
	ID        string          `json:"id"`
	Price     decimal.Decimal `json:"price"`
	Amount    decimal.Decimal `json:"amount"`
	Time      time.Time       `json:"time"`
	TradePart int             `json:"trade_part"`
}

// newDCAPurchase creates a validated DCAPurchase.
func newDCAPurchase(id string, price, amount decimal.Decimal, purchaseTime time.Time, tradePart int) (DCAPurchase, error) {
	if price.LessThanOrEqual(decimal.Zero) {
		return DCAPurchase{}, fmt.Errorf("price must be positive, got %s", price.String())
	}
	if amount.LessThanOrEqual(decimal.Zero) {
		return DCAPurchase{}, fmt.Errorf("amount must be positive, got %s", amount.String())
	}
	if tradePart < 1 {
		return DCAPurchase{}, fmt.Errorf("tradePart must be >= 1, got %d", tradePart)
	}

	return DCAPurchase{
		ID:        id,
		Price:     price,
		Amount:    amount,
		Time:      purchaseTime,
		TradePart: tradePart,
	}, nil
}

// DCASeries is the current DCA series state.
type DCASeries struct {
	Purchases         []DCAPurchase   `json:"purchases"`
	AvgEntryPrice     decimal.Decimal `json:"avg_entry_price"`
	FirstBuyTime      time.Time       `json:"first_buy_time"`
	TotalAmount       decimal.Decimal `json:"total_amount"`
	LastSellPrice     decimal.Decimal `json:"last_sell_price"`
	LastBuyPrice      decimal.Decimal `json:"last_buy_price"`
	WaitingForDip     bool            `json:"waiting_for_dip"`
	ProcessedTradeIDs map[string]bool `json:"processed_trade_ids"`
	// AllocatedQuoteAmount is the total allocated quote currency amount for this DCA series.
	// Individual trades are calculated as AllocatedQuoteAmount / MaxDcaTrades.
	AllocatedQuoteAmount decimal.Decimal `json:"allocated_quote_amount"`
}

// NewDCASeries creates a new empty DCASeries with initialized collections.
func NewDCASeries() *DCASeries {
	return &DCASeries{
		Purchases:            make([]DCAPurchase, 0),
		ProcessedTradeIDs:    make(map[string]bool),
		AvgEntryPrice:        decimal.Zero,
		TotalAmount:          decimal.Zero,
		LastSellPrice:        decimal.Zero,
		LastBuyPrice:         decimal.Zero,
		AllocatedQuoteAmount: decimal.Zero,
	}
}

// IsEmpty checks if series has no purchases.
func (s *DCASeries) IsEmpty() bool {
	return len(s.Purchases) == 0
}

// AddPurchase adds a validated purchase to the series and recalculates stats.
func (s *DCASeries) AddPurchase(id string, price, amount decimal.Decimal, purchaseTime time.Time, tradePart int) error {
	purchase, err := newDCAPurchase(id, price, amount, purchaseTime, tradePart)
	if err != nil {
		return fmt.Errorf("invalid purchase: %w", err)
	}

	s.Purchases = append(s.Purchases, purchase)
	s.recalculateStats()
	s.LastBuyPrice = price

	return nil
}

// TotalBaseAmount returns total base currency amount.
func (s *DCASeries) TotalBaseAmount() decimal.Decimal {
	if s.AvgEntryPrice.IsZero() {
		return decimal.Zero
	}
	return s.TotalAmount.Div(s.AvgEntryPrice)
}

// RemoveAmount removes amount from purchases (FIFO from end).
func (s *DCASeries) RemoveAmount(amount decimal.Decimal) {
	if amount.LessThanOrEqual(decimal.Zero) || len(s.Purchases) == 0 {
		return
	}

	remaining := amount

	for i := len(s.Purchases) - 1; i >= 0 && remaining.GreaterThan(decimal.Zero); i-- {
		purchase := s.Purchases[i]

		if purchase.Amount.LessThanOrEqual(remaining) {
			remaining = remaining.Sub(purchase.Amount)
			s.Purchases = s.Purchases[:i]
			continue
		}

		purchase.Amount = purchase.Amount.Sub(remaining)
		s.Purchases[i] = purchase
		remaining = decimal.Zero
	}

	s.recalculateStats()
}

// RemoveBaseAmount removes a base currency amount by adjusting purchase amounts proportionally.
func (s *DCASeries) RemoveBaseAmount(amountBase decimal.Decimal) {
	if amountBase.LessThanOrEqual(decimal.Zero) || len(s.Purchases) == 0 {
		return
	}

	remainingBase := amountBase

	for i := len(s.Purchases) - 1; i >= 0 && remainingBase.GreaterThan(decimal.Zero); i-- {
		purchase := s.Purchases[i]
		purchaseBase := purchase.Amount.Div(purchase.Price)

		if purchaseBase.LessThanOrEqual(remainingBase) {
			remainingBase = remainingBase.Sub(purchaseBase)
			s.Purchases = s.Purchases[:i]
			continue
		}

		// remove partial base from this purchase, convert to quote using its own price.
		quoteToRemove := remainingBase.Mul(purchase.Price)
		purchase.Amount = purchase.Amount.Sub(quoteToRemove)
		s.Purchases[i] = purchase
		remainingBase = decimal.Zero
	}

	s.recalculateStats()
}

// recalculateStats recalculates series statistics.
func (s *DCASeries) recalculateStats() {
	if len(s.Purchases) == 0 {
		s.TotalAmount = decimal.Zero
		s.AvgEntryPrice = decimal.Zero
		s.FirstBuyTime = time.Time{}
		return
	}

	totalQuoteAmount := decimal.Zero
	totalBaseAmount := decimal.Zero

	for _, purchase := range s.Purchases {
		totalQuoteAmount = totalQuoteAmount.Add(purchase.Amount)

		baseAmount := purchase.Amount.Div(purchase.Price)
		totalBaseAmount = totalBaseAmount.Add(baseAmount)
	}

	s.TotalAmount = totalQuoteAmount

	if totalBaseAmount.LessThanOrEqual(decimal.Zero) {
		s.AvgEntryPrice = decimal.Zero
	} else {
		s.AvgEntryPrice = totalQuoteAmount.Div(totalBaseAmount)
	}
	s.FirstBuyTime = s.Purchases[0].Time
}

// ShouldBuyAtPrice evaluates buy conditions at given price.
// Only considers additional buys within an active series (not for WaitingForDip logic).
func (s *DCASeries) ShouldBuyAtPrice(price decimal.Decimal, thresholds DCAThresholds) BuyDecision {
	// guard: empty series (should not happen in active series context)
	if s.IsEmpty() {
		return BuyDecision{ShouldBuy: false, Reason: "empty_series"}
	}

	if s.LastBuyPrice.IsZero() {
		return BuyDecision{ShouldBuy: false, Reason: "no_last_buy_price"}
	}

	// check: price not below last buy
	if !price.LessThan(s.LastBuyPrice) {
		return BuyDecision{ShouldBuy: false, Reason: "price_not_below_last_buy"}
	}

	// check: dip not significant
	if !IsPercentDifferenceSignificant(price, s.LastBuyPrice, thresholds.BuyThresholdPercent) {
		return BuyDecision{ShouldBuy: false, Reason: "price_not_below_threshold"}
	}

	// check: max trades reached
	if len(s.Purchases) >= thresholds.MaxTrades {
		return BuyDecision{ShouldBuy: false, Reason: "max_trades_reached"}
	}

	return BuyDecision{ShouldBuy: true, Reason: "price_dipped_below_last_buy"}
}

// ShouldTakeProfitAtPrice evaluates sell conditions at given price.
// First sell is relative to AvgEntryPrice, subsequent sells are relative to LastSellPrice.
func (s *DCASeries) ShouldTakeProfitAtPrice(price decimal.Decimal, thresholds DCAThresholds) SellDecision {
	// guard: no average price
	if s.AvgEntryPrice.IsZero() {
		return SellDecision{ShouldSell: false, Reason: "no_avg_price"}
	}

	// check: price not above average
	if !price.GreaterThan(s.AvgEntryPrice) {
		return SellDecision{ShouldSell: false, Reason: "price_not_above_avg"}
	}

	// determine reference price: AvgEntryPrice for first sell, LastSellPrice for subsequent
	reference := s.AvgEntryPrice
	if !s.LastSellPrice.IsZero() {
		reference = s.LastSellPrice
	}

	// check: price not above reference
	if !price.GreaterThan(reference) {
		return SellDecision{ShouldSell: false, Reason: "price_not_above_reference"}
	}

	// check: gain not significant
	if !IsPercentDifferenceSignificant(price, reference, thresholds.SellThresholdPercent) {
		return SellDecision{ShouldSell: false, Reason: "gain_not_significant"}
	}

	// calculate sell amount
	amount := s.calculateSellAmountInternal()

	if amount.LessThanOrEqual(decimal.Zero) {
		return SellDecision{ShouldSell: false, Reason: "no_amount_to_sell"}
	}

	totalBase := s.TotalBaseAmount()
	isFullSell := amount.Equal(totalBase)

	reason := "partial_sell_step"
	if isFullSell {
		reason = "full_sell_by_cap"
	}

	return SellDecision{
		ShouldSell: true,
		Amount:     amount,
		IsFullSell: isFullSell,
		Reason:     reason,
	}
}

// calculateSellAmountInternal calculates amount to sell.
// Returns the base amount for one step in the staircase sell strategy.
func (s *DCASeries) calculateSellAmountInternal() decimal.Decimal {
	totalBase := s.TotalBaseAmount()
	if totalBase.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero
	}

	numPurchases := len(s.Purchases)
	if numPurchases <= 0 {
		return decimal.Zero
	}

	// one "part" = total base / number of purchases
	partBase := totalBase.Div(decimal.NewFromInt(int64(numPurchases)))

	// cap at total base if part exceeds it
	if partBase.GreaterThan(totalBase) {
		return totalBase
	}

	return partBase
}

// DCAThresholds encapsulates DCA decision thresholds.
type DCAThresholds struct {
	BuyThresholdPercent  decimal.Decimal
	SellThresholdPercent decimal.Decimal
	MaxTrades            int
}

// NewDCAThresholds creates validated DCA thresholds.
func NewDCAThresholds(buyThresholdPercent, sellThresholdPercent decimal.Decimal, maxTrades int) (DCAThresholds, error) {
	if buyThresholdPercent.LessThanOrEqual(decimal.Zero) {
		return DCAThresholds{}, fmt.Errorf("buyThresholdPercent must be positive, got %s", buyThresholdPercent.String())
	}
	if sellThresholdPercent.LessThanOrEqual(decimal.Zero) {
		return DCAThresholds{}, fmt.Errorf("sellThresholdPercent must be positive, got %s", sellThresholdPercent.String())
	}
	if maxTrades < 1 {
		return DCAThresholds{}, fmt.Errorf("maxTrades must be >= 1, got %d", maxTrades)
	}

	return DCAThresholds{
		BuyThresholdPercent:  buyThresholdPercent,
		SellThresholdPercent: sellThresholdPercent,
		MaxTrades:            maxTrades,
	}, nil
}

// BuyDecision represents a buy decision result.
type BuyDecision struct {
	ShouldBuy bool
	Reason    string
}

// SellDecision represents a sell decision result.
type SellDecision struct {
	ShouldSell bool
	Amount     decimal.Decimal
	IsFullSell bool
	Reason     string
}

// Helper functions

// IsPercentDifferenceSignificant checks if percentage difference exceeds threshold.
func IsPercentDifferenceSignificant(currentPrice, referencePrice, thresholdPercent decimal.Decimal) bool {
	if referencePrice.IsZero() {
		return false
	}

	diff := currentPrice.Sub(referencePrice)
	percentageDiff := diff.Div(referencePrice)
	absPercentageDiff := percentageDiff.Abs()
	absPercentageDiffHundred := absPercentageDiff.Mul(decimal.NewFromInt(percentageMultiplier))

	return absPercentageDiffHundred.GreaterThanOrEqual(thresholdPercent)
}

// PercentageDiff returns percentage difference between current and reference values.
func PercentageDiff(current, reference decimal.Decimal) decimal.Decimal {
	if reference.IsZero() {
		return decimal.Zero
	}
	return current.Sub(reference).Div(reference).Mul(decimal.NewFromInt(percentageMultiplier))
}
