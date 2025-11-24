# Marti - Cryptocurrency Trading Bot

[![Go Reference](https://pkg.go.dev/badge/github.com/vadiminshakov/marti.svg)](https://pkg.go.dev/github.com/vadiminshakov/marti)
![Tests](https://github.com/vadiminshakov/marti/workflows/tests/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/vadiminshakov/marti)](https://goreportcard.com/report/github.com/vadiminshakov/marti)

![marti](https://github.com/vadiminshakov/marti/blob/main/logo.png)

Marti is a cryptocurrency trading bot with *DCA* and *AI* strategies for multiple exchanges.

## live: https://llmtrade.tech

![Screenshot](https://github.com/vadiminshakov/marti/blob/main/screenshot.png)

## Installation

```bash
go install github.com/vadiminshakov/marti/cmd@latest
```

### Run

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

### Configuration

See [config.yaml](config.yaml) for a complete example.


### Supported LLM Providers

The AI strategy works with any OpenAI-compatible API.

## Testing with Historical Data

```bash
BINANCE_API_KEY=your_api_key BINANCE_API_SECRET=your_api_secret go test -v ./historytest
```

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
