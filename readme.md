# Marti

![](https://github.com/vadiminshakov/marti/workflows/tests/badge.svg)

![marti](https://github.com/vadiminshakov/marti/blob/main/logo.png)

A cryptocurrency trading bot using DCA (Dollar Cost Average) strategy with profit-taking and market re-entry capabilities.

## Features

- Multi-exchange support (Binance, Bybit)
- DCA strategy with configurable parameters
- Automatic profit-taking when price rises by a set percentage
- Market re-entry after price dips
- Modular architecture for easy extension to other platforms

## Quick Start

```bash
# Set API credentials
export BINANCE_API_KEY=your_api_key
export BINANCE_API_SECRET=your_api_secret
Or
# Set Bybit API credentials
export BYBIT_API_KEY=your_api_key
export BYBIT_API_SECRET=your_api_secret

# Build and run
go build
./marti --config config.yaml
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

## License

MIT