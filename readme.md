# Marti - Cryptocurrency Trading Bot Framework

[![Go Reference](https://pkg.go.dev/badge/github.com/vadiminshakov/marti.svg)](https://pkg.go.dev/github.com/vadiminshakov/marti)
![Tests](https://github.com/vadiminshakov/marti/workflows/tests/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/vadiminshakov/marti)](https://goreportcard.com/report/github.com/vadiminshakov/marti)

![marti](https://github.com/vadiminshakov/marti/blob/main/logo.png)

Marti is a flexible cryptocurrency trading bot framework that implements Dollar Cost Averaging (DCA) strategy for automated trading across multiple exchanges. It provides a comprehensive solution for algorithmic trading with configurable parameters, real-time price monitoring, and robust trade execution.

## Features

- **Multi-Exchange Support**: Seamless integration with Binance and Bybit exchanges
- **DCA Strategy**: Configurable Dollar Cost Averaging with automatic trade execution
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

```yaml
- pair: BTC_USDT                     # Trading pair in COIN1_COIN2 format
  platform: binance                  # Exchange (binance or bybit supported)
  amount: 38                         # Percentage of balance to use (0-100)
  pollpriceinterval: 5m              # Interval between price checks
  max_dca_trades: 4                  # Maximum number of DCA trades
  dca_percent_threshold_buy: 3.5     # Price drop percentage to trigger buy
  dca_percent_threshold_sell: 66     # Price rise percentage to trigger sell
```

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
