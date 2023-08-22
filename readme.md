![](https://github.com/vadiminshakov/marti/workflows/tests/badge.svg)

![marti](https://github.com/vadiminshakov/marti/blob/main/img.png)

Simple and reliable bot for cryptocurrency trading. Currently, trading is implemented on Binance. The modular architecture was initially designed to allow for the expansion of trading platforms: the core can be integrated with any platform, including the stock market.

To start the bot, simply specify the configuration file:
```
export APIKEY=your_api_key
export SECRETKEY=your_api_secret
go build
./marti --config config.yaml
```

**Configuration:**

This application has a configuration that can be customized using YAML file:

_config.yaml_
```
# The trading pair. The pair should be in the format COIN1_COIN2.
- pair: BTC_USDT

# The minimum window size for statistical analysis.
  minwindow: 100
  
# The number of hours in the past to be used for statistical calculations.
  stathours: 120
  
# The percentage of available balance to be used for trading. The value should be in the range of 0 to 100.
  usebalance: 38
  
# The time interval between rebalancing (market state reassessment).
  rebalanceinterval: 16h
  
# The time interval between polling market prices to make trading decision (buy/sell/do nothing).
  pollpriceinterval: 5m
```