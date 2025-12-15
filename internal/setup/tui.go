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
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Background(highlight).
			Padding(1, 2).
			Bold(true).
			MarginBottom(1)

	stepStyle = lipgloss.NewStyle().
			Foreground(special).
			Bold(true).
			MarginTop(1).
			MarginBottom(0)
)

// RunTUI launches the terminal configuration wizard.
func RunTUI() error {
	var (
		strategy         string
		platform         string
		pair             string
		marketType       string
		pollIntervalStr  string
		amountStr        string
		maxDcaTradesStr  string
		buyThresholdStr  string
		sellThresholdStr string
		apiURL           string
		apiKey           string
		model            string
		primaryTimeframe string
		confirm          bool
	)

	// defaults
	amountStr = "10"
	pollIntervalStr = "5m"
	apiURL = "https://openrouter.ai/api/v1/chat/completions"
	model = "deepseek/deepseek-v3.2-exp"
	primaryTimeframe = "3m"
	maxDcaTradesStr = "15"
	buyThresholdStr = "3.5"
	sellThresholdStr = "0.75"

	// missing AI defaults
	higherTimeframe := "15m"
	primaryLookbackPeriods := "50"
	higherLookbackPeriods := "60"
	maxLeverageStr := "10"

	// step 1: welcome
	fmt.Print("\033[H\033[2J") // Clear screen
	fmt.Println(headerStyle.Render("MARTI CONFIG WIZARD"))
	fmt.Println(lipgloss.NewStyle().Foreground(subtle).Render("Let's get your bot automated in style.\n"))

	// strategy
	fmt.Println(stepStyle.Render("STEP 1: STRATEGY"))
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose your trading strategy").
				Options(
					huh.NewOption("DCA (Dollar Cost Averaging)", "dca"),
					huh.NewOption("AI (LLM-based)", "ai"),
				).
				Value(&strategy),
		),
	).Run()
	if err != nil {
		return err
	}

	// platform
	fmt.Print("\033[H\033[2J")
	fmt.Println(headerStyle.Render("MARTI CONFIG WIZARD"))
	fmt.Println(stepStyle.Render("STEP 2: PLATFORM"))
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select Exchange Platform").
				Options(
					huh.NewOption("Binance", "binance"),
					huh.NewOption("Bybit", "bybit"),
					huh.NewOption("Hyperliquid", "hyperliquid"),
					huh.NewOption("Simulation", "simulate"),
				).
				Value(&platform),
		),
	).Run()
	if err != nil {
		return err
	}

	// pair
	fmt.Print("\033[H\033[2J")
	fmt.Println(headerStyle.Render("MARTI CONFIG WIZARD"))
	fmt.Println(stepStyle.Render("STEP 3: ASSET"))
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Trading Pair").
				Description("Must contain underscore (e.g. BTC_USDT)").
				Value(&pair).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("pair cannot be empty")
					}
					if !containsUnderscore(s) {
						return fmt.Errorf("invalid format: must be BASE_QUOTE (e.g. BTC_USDT)")
					}
					return nil
				}),
		),
	).Run()
	if err != nil {
		return err
	}

	// market type
	fmt.Print("\033[H\033[2J")
	fmt.Println(headerStyle.Render("MARTI CONFIG WIZARD"))
	fmt.Println(stepStyle.Render("STEP 4: MARKET TYPE"))
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Spot or Margin?").
				Options(
					huh.NewOption("Spot", "spot"),
					huh.NewOption("Margin", "margin"),
				).
				Value(&marketType),
		),
	).Run()
	if err != nil {
		return err
	}

	// poll interval
	fmt.Print("\033[H\033[2J")
	fmt.Println(headerStyle.Render("MARTI CONFIG WIZARD"))
	fmt.Println(stepStyle.Render("STEP 5: TIMING"))
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Poll Price Interval").
				Description("Duration string (e.g. 30s, 1m, 5m)").
				Value(&pollIntervalStr).
				Validate(func(s string) error {
					_, err := time.ParseDuration(s)
					return err
				}),
		),
	).Run()
	if err != nil {
		return err
	}

	// strategy specifics
	if strategy == "dca" {
		fmt.Print("\033[H\033[2J")
		fmt.Println(headerStyle.Render("MARTI CONFIG WIZARD"))
		fmt.Println(stepStyle.Render("STEP 6: DCA SETTINGS"))
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Amount % per trade").
					Description("Percentage of quote balance (1-100)").
					Value(&amountStr).
					Validate(validateAmount),
				huh.NewInput().
					Title("Max DCA Trades").
					Description("Maximum number of additional DCA buy orders (e.g. 15)").
					Value(&maxDcaTradesStr),
				huh.NewInput().
					Title("Buy Price Drop %").
					Description("Price drop to trigger safety order (e.g. 3.5)").
					Value(&buyThresholdStr),
				huh.NewInput().
					Title("Sell Take Profit %").
					Description("Price rise to take profit (e.g. 0.75)").
					Value(&sellThresholdStr),
			),
		).Run()
		if err != nil {
			return err
		}
	} else if strategy == "ai" {
		fmt.Print("\033[H\033[2J")
		fmt.Println(headerStyle.Render("MARTI CONFIG WIZARD"))
		fmt.Println(stepStyle.Render("STEP 6: AI SETTINGS"))

		aiFields := []huh.Field{
			huh.NewInput().
				Title("LLM API URL").
				Value(&apiURL),
			huh.NewInput().
				Title("LLM API Key").
				Value(&apiKey).
				EchoMode(huh.EchoModePassword),
			huh.NewInput().
				Title("Model Name").
				Value(&model),
			huh.NewInput().
				Title("Primary Timeframe").
				Value(&primaryTimeframe),
			huh.NewInput().
				Title("Higher Timeframe").
				Value(&higherTimeframe),
			huh.NewInput().
				Title("Primary Lookback Periods").
				Description("Min 50").
				Value(&primaryLookbackPeriods),
			huh.NewInput().
				Title("Higher Lookback Periods").
				Description("Min 20").
				Value(&higherLookbackPeriods),
		}

		if marketType == "margin" {
			aiFields = append(aiFields, huh.NewInput().
				Title("Max Leverage").
				Value(&maxLeverageStr),
			)
		}

		err = huh.NewForm(huh.NewGroup(aiFields...)).Run()
		if err != nil {
			return err
		}
	}

	// confirmation
	fmt.Print("\033[H\033[2J")
	fmt.Println(headerStyle.Render("MARTI CONFIG WIZARD"))
	fmt.Println(stepStyle.Render("FINAL CONFIRMATION"))

	// show summary
	summary := fmt.Sprintf(
		"Strategy: %s\nPlatform: %s\nPair: %s\nMarket: %s\nInterval: %s\n",
		strategy, platform, pair, marketType, pollIntervalStr,
	)
	fmt.Println(lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1).Render(summary))

	err = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Save Configuration?").
				Affirmative("Yes, save and start").
				Negative("No, exit").
				Value(&confirm),
		),
	).Run()
	if err != nil {
		return err
	}

	if !confirm {
		return fmt.Errorf("setup cancelled by user")
	}

	// generate config
	pollInterval, _ := time.ParseDuration(pollIntervalStr)

	cfgTmp := config.ConfigTmp{
		Platform:          platform,
		Pair:              pair,
		Strategy:          strategy,
		MarketTypeStr:     marketType,
		PollPriceInterval: pollInterval,
	}

	if strategy == "dca" {
		cfgTmp.Amount = amountStr
		cfgTmp.MaxDcaTradesStr = maxDcaTradesStr
		cfgTmp.DcaPercentThresholdBuyStr = buyThresholdStr
		cfgTmp.DcaPercentThresholdSellStr = sellThresholdStr
	} else if strategy == "ai" {
		cfgTmp.LLMAPIURL = apiURL
		cfgTmp.LLMAPIKey = apiKey
		cfgTmp.Model = model
		cfgTmp.PrimaryTimeframe = primaryTimeframe
		cfgTmp.HigherTimeframe = higherTimeframe
		cfgTmp.PrimaryLookbackPeriodsStr = primaryLookbackPeriods
		cfgTmp.HigherLookbackPeriodsStr = higherLookbackPeriods
		if marketType == "margin" {
			cfgTmp.MaxLeverageStr = maxLeverageStr
		}
	}

	configs := []config.ConfigTmp{cfgTmp}

	data, err := yaml.Marshal(configs)
	if err != nil {
		return fmt.Errorf("failed to generate yaml: %w", err)
	}

	// write to config.gen.yaml
	filename := "config.gen.yaml"
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to save config file: %w", err)
	}

	fmt.Println(lipgloss.NewStyle().Foreground(special).Render(fmt.Sprintf("\n✓ Configuration saved to %s\nStarting bot...", filename)))
	time.Sleep(1500 * time.Millisecond) // small pause to read success message
	return nil
}

func validateAmount(s string) error {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return fmt.Errorf("must be a valid number")
	}
	if d.LessThan(decimal.NewFromInt(1)) || d.GreaterThan(decimal.NewFromInt(100)) {
		return fmt.Errorf("must be between 1 and 100")
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
