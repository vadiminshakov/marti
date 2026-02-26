package setup

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/config"
	"gopkg.in/yaml.v3"
)

var (
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	muted     = lipgloss.AdaptiveColor{Light: "#888888", Dark: "#888888"}

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Background(highlight).
			Padding(1, 2).
			Bold(true).
			MarginBottom(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(special).
			Bold(true)

	mutedStyle = lipgloss.NewStyle().
			Foreground(muted)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(highlight).
			Padding(0, 1)
)

// RunTUI launches the terminal configuration wizard.
func RunTUI() error {
	fmt.Print("\033[H\033[2J")
	fmt.Println(headerStyle.Render("  MARTI  ·  Config Wizard  "))
	fmt.Println(mutedStyle.Render("  Use ↑/↓ to navigate, Enter to confirm, ESC to go back.\n"))

	var (
		strategy         = "dca"
		platform         = "binance"
		pair             = "BTC_USDT"
		marketType       = "spot"
		pollIntervalStr  = "5m"
		amountStr        = "10"
		maxDcaTradesStr  = "15"
		buyThresholdStr  = "3.5"
		sellThresholdStr = "0.75"
		apiURL           = "https://openrouter.ai/api/v1/chat/completions"
		apiKey           string
		model            = "deepseek/deepseek-v3-0324"
		primaryTimeframe = "3m"
	)

	mainForm := huh.NewForm(
		// ── Group 1: Strategy & Platform ─────────────────────────────────────
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Trading strategy").
				Description("DCA: rule-based averaging down. AI: LLM makes buy/sell decisions.").
				Options(
					huh.NewOption("DCA  — Dollar-Cost Averaging (recommended for beginners)", "dca"),
					huh.NewOption("AI   — LLM-based autonomous trading", "ai"),
				).
				Value(&strategy),

			huh.NewSelect[string]().
				Title("Exchange platform").
				Options(
					huh.NewOption("Binance", "binance"),
					huh.NewOption("Bybit", "bybit"),
					huh.NewOption("Hyperliquid", "hyperliquid"),
					huh.NewOption("Simulation (paper trading, no real money)", "simulate"),
				).
				Value(&platform),
		),

		// ── Group 2: Pair, Market, Interval ──────────────────────────────────
		huh.NewGroup(
			huh.NewInput().
				Title("Trading pair").
				Description("Format: BASE_QUOTE  (e.g. BTC_USDT, ETH_USDT)").
				Value(&pair).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("trading pair cannot be empty")
					}
					if !containsUnderscore(s) {
						return fmt.Errorf("must use underscore separator: BTC_USDT")
					}
					return nil
				}),

			huh.NewSelect[string]().
				Title("Market type").
				Description("Spot: trade with owned funds. Margin: trade with borrowed funds (higher risk).").
				Options(
					huh.NewOption("Spot  — buy/sell with your own balance", "spot"),
					huh.NewOption("Margin — leveraged trading", "margin"),
				).
				Value(&marketType),

			huh.NewInput().
				Title("Price check interval").
				Description("How often to fetch the latest price (e.g. 30s, 1m, 5m).").
				Value(&pollIntervalStr).
				Validate(func(s string) error {
					if _, err := time.ParseDuration(s); err != nil {
						return fmt.Errorf("invalid duration — use format like 30s, 1m, 5m")
					}
					return nil
				}),
		),

		// ── Group 3a: DCA settings (hidden when strategy == "ai") ────────────
		huh.NewGroup(
			huh.NewInput().
				Title("Trade amount (% of balance)").
				Description("Percentage of your quote balance to allocate for the full DCA series (1–100).").
				Value(&amountStr).
				Validate(validateAmount),

			huh.NewInput().
				Title("Max DCA orders").
				Description("How many additional safety orders can be placed per series (e.g. 15).").
				Value(&maxDcaTradesStr).
				Validate(validatePositiveInt),

			huh.NewInput().
				Title("Safety order trigger — price drop %").
				Description("Place a new buy order when price drops by this % from last buy (e.g. 3.5).").
				Value(&buyThresholdStr).
				Validate(validatePositiveDecimal),

			huh.NewInput().
				Title("Take-profit — price rise %").
				Description("Close the position when average entry is exceeded by this % (e.g. 0.75).").
				Value(&sellThresholdStr).
				Validate(validatePositiveDecimal),
		).WithHideFunc(func() bool { return strategy == "ai" }),

		// ── Group 3b: AI settings (hidden when strategy == "dca") ────────────
		huh.NewGroup(
			huh.NewInput().
				Title("LLM API URL").
				Description("OpenAI-compatible endpoint. Default: OpenRouter.").
				Value(&apiURL).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("API URL cannot be empty")
					}
					return nil
				}),

			huh.NewInput().
				Title("LLM API Key").
				Description("Your API key — stored only in the local config file.").
				EchoMode(huh.EchoModePassword).
				Value(&apiKey).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("API key is required for AI strategy")
					}
					return nil
				}),

			huh.NewInput().
				Title("Model name").
				Description("LLM model identifier (e.g. deepseek/deepseek-v3-0324, gpt-4o).").
				Value(&model).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("model name cannot be empty")
					}
					return nil
				}),

			huh.NewSelect[string]().
				Title("Primary timeframe").
				Description("Candlestick interval the AI analyses most closely.").
				Options(
					huh.NewOption("1m  — 1 minute", "1m"),
					huh.NewOption("3m  — 3 minutes (default)", "3m"),
					huh.NewOption("5m  — 5 minutes", "5m"),
					huh.NewOption("15m — 15 minutes", "15m"),
					huh.NewOption("1h  — 1 hour", "1h"),
				).
				Value(&primaryTimeframe),
		).WithHideFunc(func() bool { return strategy == "dca" }),
	).WithTheme(huh.ThemeDracula())

	if err := mainForm.Run(); err != nil {
		return err
	}

	// ── Build summary after all fields are filled ─────────────────────────────
	summary := buildSummary(strategy, platform, pair, marketType, pollIntervalStr,
		amountStr, maxDcaTradesStr, buyThresholdStr, sellThresholdStr,
		apiURL, model, primaryTimeframe)

	confirm := true
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Save configuration?").
				Description(summary).
				Affirmative("Yes, save and start").
				Negative("Cancel").
				Value(&confirm),
		),
	).WithTheme(huh.ThemeDracula())

	if err := confirmForm.Run(); err != nil {
		return err
	}

	if !confirm {
		return fmt.Errorf("setup cancelled by user")
	}

	// ── Build config ──────────────────────────────────────────────────────────
	pollInterval, _ := time.ParseDuration(pollIntervalStr)

	cfgTmp := config.ConfigTmp{
		Platform:          platform,
		Pair:              pair,
		Strategy:          strategy,
		MarketTypeStr:     marketType,
		PollPriceInterval: pollInterval,
	}

	switch strategy {
	case "dca":
		cfgTmp.Amount = amountStr
		cfgTmp.MaxDcaTradesStr = maxDcaTradesStr
		cfgTmp.DcaPercentThresholdBuyStr = buyThresholdStr
		cfgTmp.DcaPercentThresholdSellStr = sellThresholdStr
	case "ai":
		cfgTmp.LLMAPIURL = apiURL
		cfgTmp.LLMAPIKey = apiKey
		cfgTmp.Model = model
		cfgTmp.PrimaryTimeframe = primaryTimeframe
		cfgTmp.HigherTimeframe = "15m"
		cfgTmp.PrimaryLookbackPeriodsStr = "50"
		cfgTmp.HigherLookbackPeriodsStr = "60"
		if marketType == "margin" {
			cfgTmp.MaxLeverageStr = "10"
		}
	}

	data, err := yaml.Marshal([]config.ConfigTmp{cfgTmp})
	if err != nil {
		return fmt.Errorf("failed to generate yaml: %w", err)
	}

	const filename = "config.gen.yaml"
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to save config file: %w", err)
	}

	fmt.Println(boxStyle.Render(
		labelStyle.Render("✓ Config saved → ") + filename + "\n" +
			mutedStyle.Render("  Starting bot…"),
	))
	time.Sleep(1200 * time.Millisecond)
	return nil
}

// buildSummary returns a human-readable config summary for the confirmation screen.
// All string arguments are plain values (not format verbs), so % chars are safe.
func buildSummary(strategy, platform, pair, marketType, pollInterval,
	amount, maxTrades, buyThr, sellThr,
	apiURL, model, primaryTF string,
) string {
	base := "Strategy: " + strategy +
		"   Platform: " + platform +
		"\nPair: " + pair +
		"   Market: " + marketType +
		"   Interval: " + pollInterval + "\n"

	switch strategy {
	case "dca":
		return base +
			"Amount: " + amount + "%" +
			"   Max orders: " + maxTrades +
			"   Buy at: -" + buyThr + "%" +
			"   Sell at: +" + sellThr + "%"
	case "ai":
		return base +
			"Model: " + model +
			"   Timeframe: " + primaryTF +
			"\nAPI: " + shortenURL(apiURL)
	default:
		return base
	}
}

func shortenURL(u string) string {
	if len(u) > 40 {
		return u[:37] + "…"
	}
	return u
}

// ── Validators ────────────────────────────────────────────────────────────────

func validateAmount(s string) error {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return fmt.Errorf("must be a valid number (e.g. 10)")
	}
	if d.LessThan(decimal.NewFromInt(1)) || d.GreaterThan(decimal.NewFromInt(100)) {
		return fmt.Errorf("must be between 1 and 100")
	}
	return nil
}

func validatePositiveInt(s string) error {
	d, err := decimal.NewFromString(s)
	if err != nil || !d.IsPositive() || d.Exponent() < 0 {
		return fmt.Errorf("must be a positive integer (e.g. 15)")
	}
	return nil
}

func validatePositiveDecimal(s string) error {
	d, err := decimal.NewFromString(s)
	if err != nil || !d.IsPositive() {
		return fmt.Errorf("must be a positive number (e.g. 3.5)")
	}
	return nil
}

func containsUnderscore(s string) bool {
	for _, r := range s {
		if r == '_' {
			return true
		}
	}
	return false
}
