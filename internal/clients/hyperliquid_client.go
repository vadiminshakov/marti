package clients

import (
	"context"
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
	hyperliquid "github.com/sonirico/go-hyperliquid"
)

type HyperliquidClient struct {
	exchange    *hyperliquid.Exchange
	accountAddr string
}

func NewHyperliquidClient(privateKeyHex string, baseURL string) (*HyperliquidClient, error) {
	key := privateKeyHex
	// NewExchange accepts a *ecdsa.PrivateKey, derive account address from it.
	if len(key) >= 2 && (key[:2] == "0x" || key[:2] == "0X") {
		key = key[2:]
	}

	privateKey, err := crypto.HexToECDSA(key)
	if err != nil {
		return nil, err
	}

	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("error casting public key to ECDSA")
	}
	accountAddr := crypto.PubkeyToAddress(*pubECDSA).Hex()

	// build exchange; Info and SpotMeta are fetched lazily by the SDK
	ex := hyperliquid.NewExchange(
		context.Background(),
		privateKey,
		baseURL,
		nil,
		"",
		accountAddr,
		nil,
	)

	return &HyperliquidClient{exchange: ex, accountAddr: accountAddr}, nil
}

func (c *HyperliquidClient) Exchange() *hyperliquid.Exchange { return c.exchange }
func (c *HyperliquidClient) AccountAddress() string          { return c.accountAddr }
