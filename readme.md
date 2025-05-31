![](https://github.com/vadiminshakov/marti/workflows/tests/badge.svg)

![marti](https://github.com/vadiminshakov/marti/blob/main/img.png)

Simple and reliable bot for cryptocurrency trading. The bot supports multiple trading platforms (currently Binance and Bybit) and uses a DCA (Dollar Cost Average) strategy with the ability to sell at profit and re-enter the market after a price dip. The modular architecture allows for the expansion of trading platforms: the core can be integrated with any platform, including the stock market.

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
```yaml
# The trading pair. The pair should be in the format COIN1_COIN2.
- pair: BTC_USDT
  # The trading platform (binance or bybit)
  platform: binance
  # The percentage of available balance to be used for trading. The value should be in the range of 0 to 100.
  amount: 38
  # The time interval between polling market prices to make trading decision (buy/sell/do nothing).
  pollpriceinterval: 5m
  # The maximum number of DCA trades to be executed.
  max_dca_trades: 3
  # The percentage of the price drop required to trigger a buy.
  dca_percent_threshold_buy: 3.5
  # The percentage of the price rise required to trigger a sell.
  dca_percent_threshold_sell: 66
```