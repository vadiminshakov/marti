# Marti - Cryptocurrency Trading Bot Framework

[![Go Reference](https://pkg.go.dev/badge/github.com/vadiminshakov/marti.svg)](https://pkg.go.dev/github.com/vadiminshakov/marti)
![Tests](https://github.com/vadiminshakov/marti/workflows/tests/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/vadiminshakov/marti)](https://goreportcard.com/report/github.com/vadiminshakov/marti)

![marti](https://github.com/vadiminshakov/marti/blob/main/logo.png)

Marti is a flexible cryptocurrency trading bot framework that supports multiple trading strategies including Dollar Cost Averaging (DCA) and AI-powered technical analysis for automated trading across multiple exchanges. It provides a comprehensive solution for algorithmic trading with configurable parameters, real-time price monitoring, and robust trade execution.

## Features

- **Multi-Exchange Support**: Seamless integration with Binance and Bybit exchanges
- **Multiple Trading Strategies**:
  - **DCA Strategy**: Configurable Dollar Cost Averaging with automatic trade execution
  - **AI Strategy**: LLM-powered technical analysis

- **Profit Management**: Intelligent profit-taking when price rises by configured thresholds
- **Market Re-entry**: Automatic market re-entry after price corrections
- **Modular Architecture**: Extensible design for adding new exchanges and strategies
- **Backtesting Support**: Historical data testing capabilities for strategy validation

## Installation

```bash
go install github.com/vadiminshakov/marti/cmd@latest
```

## Quick Start (Standalone Application)

```bash
# Set API credentials
export BINANCE_API_KEY=your_api_key
export BINANCE_API_SECRET=your_api_secret
# Or for Bybit:
# export BYBIT_API_KEY=your_api_key  
# export BYBIT_API_SECRET=your_api_secret

# Run
marti --config config.yaml
```

## Configuration

Create a `config.yaml` file:

### DCA Strategy Configuration

```yaml
- pair: BTC_USDT                     # Trading pair in COIN1_COIN2 format
  platform: binance                  # Exchange (binance or bybit supported)
  strategy: dca                      # Strategy type
  amount: 38                         # Percentage of balance to use (0-100)
  pollpriceinterval: 5m              # Interval between price checks
  max_dca_trades: 4                  # Maximum number of DCA trades
  dca_percent_threshold_buy: 3.5     # Price drop percentage to trigger buy
  dca_percent_threshold_sell: 66     # Price rise percentage to trigger sell
```

### AI Strategy Configuration

```yaml
- pair: BTC_USDT
  platform: binance                  # Or 'simulate' for testing
  strategy: ai                       # AI-powered trading strategy
  llm_api_url: "https://openrouter.ai/api/v1/chat/completions"
  llm_api_key: "sk-your-api-key-here"
  model: "deepseek/deepseek-chat"    # LLM model to use
  primary_timeframe: "3m"            # Primary timeframe for analysis
  higher_timeframe: "15m"            # Higher timeframe for trend confirmation
  lookback_periods: 100              # Historical candles to analyze (min 50)
  pollpriceinterval: 3m              # Decision-making interval
```

### Simulation Mode Examples

You can use `platform: simulate` to run strategies without hitting a real exchange API (helpful for local testing/backtesting):

```yaml
# DCA (simulate)
- pair: BTC_USDT
  platform: simulate
  strategy: dca
  amount: 10
  pollpriceinterval: 2s
  max_dca_trades: 8
  dca_percent_threshold_buy: 2
  dca_percent_threshold_sell: 3

# AI (simulate)
- pair: SOL_USDT
  platform: simulate
  strategy: ai
  market_type: spot
  llm_api_url: "https://openrouter.ai/api/v1/chat/completions"
  llm_api_key: "sk-your-api-key-here"    # Placeholder, set your own key
  model: "deepseek/deepseek-chat"
  primary_timeframe: "15m"
  higher_timeframe: "1d"
  primary_lookback_periods: 80            # >= 50 required
  higher_lookback_periods: 120
  pollpriceinterval: 15s
```

## AI Trading Strategy

The AI trading strategy uses Large Language Models (LLMs) to analyze market data and make informed trading decisions based on technical analysis. This approach combines traditional technical indicators with advanced pattern recognition and multi-timeframe analysis.

### Supported LLM Providers

The AI strategy works with any OpenAI-compatible API:
- **OpenRouter**: Access to multiple models (DeepSeek, GPT-4, Claude, etc.)
- **OpenAI**: Direct GPT-4 or GPT-3.5 integration
- **Local LLMs**: Any local model with OpenAI-compatible API (e.g., Ollama, LM Studio)

## Testing with Historical Data

```bash
BINANCE_API_KEY=your_api_key BINANCE_API_SECRET=your_api_secret go test -v ./historytest
```

## Package Structure

### Core Packages

- **`config`**: Configuration management with support for YAML files and CLI arguments
- **`internal`**: Core trading bot implementation and strategy interfaces
- **`internal/entity`**: Data structures for trading pairs, actions, and events
- **`internal/clients`**: Exchange client implementations (Binance, Bybit)
- **`internal/services`**: Trading services including pricers, traders, and strategies

### Testing Packages

- **`historytest`**: Backtesting utilities with historical market data
- **`mocks`**: Generated mocks for testing

## API Documentation

For detailed API documentation, visit [pkg.go.dev/github.com/vadiminshakov/marti](https://pkg.go.dev/github.com/vadiminshakov/marti).

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`) 
5. Open a Pull Request

## Disclaimer

This software is for educational and research purposes only. Cryptocurrency trading involves substantial risk of loss. The authors are not responsible for any financial losses incurred through the use of this software. Always test thoroughly with small amounts before using in production.

## License

Apache 2.0
