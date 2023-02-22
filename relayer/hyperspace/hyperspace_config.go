// Package rly provides an interface to the cosmos relayer running in a Docker container.
package hyperspace

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/strangelove-ventures/interchaintest/v7/chain/polkadot"
	"github.com/strangelove-ventures/interchaintest/v7/ibc"
	bip32 "github.com/tyler-smith/go-bip32"
	bip39 "github.com/tyler-smith/go-bip39"
)

type RelayerCoreConfig struct {
	PrometheusEndpoint string
}

type RelayerSubstrateChainConfig struct {
	Type             string   `toml:"type"`
	Name             string   `toml:"name"`
	ParaID           uint32   `toml:"para_id"`
	ParachainRPCURL  string   `toml:"parachain_rpc_url"`
	RelayChainRPCURL string   `toml:"relay_chain_rpc_url"`
	BeefyActivation  uint32   `toml:"beefy_activation_block"`
	CommitmentPrefix string   `toml:"commitment_prefix"`
	PrivateKey       string   `toml:"private_key"`
	SS58Version      uint8    `toml:"ss58_version"`
	ChannelWhitelist []string `toml:"channel_whitelist"`
	FinalityProtocol string   `toml:"finality_protocol"`
	KeyType          string   `toml:"key_type"`
}

type KeyEntry struct {
	PublicKey  string `toml:"public_key"`
	PrivateKey string `toml:"private_key"`
	Account    string `toml:"account"`
	Address    []byte `toml:"address"`
}

type RelayerCosmosChainConfig struct {
	Type          string   `toml:"type"`
	Name          string   `toml:"name"`
	RPCUrl        string   `toml:"rpc_url"`
	GRPCUrl       string   `toml:"grpc_url"`
	WebsocketURL  string   `toml:"websocket_url"`
	ChainID       string   `toml:"chain_id"`
	AccountPrefix string   `toml:"account_prefix"`
	StorePrefix   string   `toml:"store_prefix"`
	MaxTxSize     uint64   `toml:"max_tx_size"`
	WasmCodeID    string   `toml:"wasm_code_id"`
	Keybase       KeyEntry `toml:"keybase"`
}

const (
	HyperspaceDefaultContainerImage   = "hyperspace"
	HyperspaceDefaultContainerVersion = "local"
)

func GenKeyEntry(bech32Prefix, coinType, mnemonic string) KeyEntry {
	coinType64, err := strconv.ParseUint(coinType, 10, 32)
	if err != nil {
		return KeyEntry{}
	}
	algo := keyring.SignatureAlgo(hd.Secp256k1)
	hdPath := hd.CreateHDPath(uint32(coinType64), 0, 0).String()

	// create master key and derive first key for keyring
	derivedPriv, err := algo.Derive()(mnemonic, "", hdPath)
	if err != nil {
		return KeyEntry{}
	}

	privKey := algo.Generate()(derivedPriv)
	address := types.AccAddress(privKey.PubKey().Address())
	bech32Addr := types.MustBech32ifyAddressBytes(bech32Prefix, address)

	// Derive extended private key
	seed := bip39.NewSeed(mnemonic, "")
	masterKey, _ := bip32.NewMasterKey(seed)
	purposeKey, _ := masterKey.NewChildKey(0x8000002C)                        // 44'
	coinTypeKey, _ := purposeKey.NewChildKey(0x80000000 + uint32(coinType64)) // 118'
	accountKey, _ := coinTypeKey.NewChildKey(0x80000000)                      // 0'
	changeKey, _ := accountKey.NewChildKey(0)                                 // 0
	indexKey, _ := changeKey.NewChildKey(0)                                   // 0

	return KeyEntry{
		PublicKey:  indexKey.PublicKey().B58Serialize(), // i.e. "xpub6GNKSnPmR5zN3Ef3EqYkSJTZzjzGecb1n1SqJRUNnoFPsyxviG7QyoVzjEjP3gfqRu7AvRrEZMfXJazz8pZgmYP6yvvdRqC2pWmWpeQTMBP"
		PrivateKey: indexKey.B58Serialize(),             // i.e. "xprvA3Ny3GrsaiS4pkaa8p1k5AWqSi9nF9sAQnXEW34mETiR1BdnAioAS1BWsx3uAXKT3NbY6cpY2mQL6N7R8se1GVHqNkpjwc7rv5VRaQ9x8EB"
		Account:    bech32Addr,                          // i.e. "cosmos1pyxjp07wc207l7jecyr3wcmq9cr54tqwhcwugm"
		Address:    address.Bytes(),                     // i.e. [9, 13, 32, 191, 206, 194, 159, 239, 250, 89, 193, 7, 23, 99, 96, 46, 7, 74, 172, 14]
	}
}

func ChainConfigToHyperspaceRelayerChainConfig(chainConfig ibc.ChainConfig, keyName, rpcAddr, grpcAddr string) interface{} {
	chainType := chainConfig.Type
	if chainType == "polkadot" || chainType == "parachain" || chainType == "relaychain" { //nolint:goconst
		chainType = "parachain"
	}

	if chainType == "parachain" { //nolint:gocritic
		addrs := strings.Split(rpcAddr, ",")
		paraRPCAddr := rpcAddr
		relayRPCAddr := grpcAddr
		if len(addrs) > 1 {
			paraRPCAddr, relayRPCAddr = addrs[0], addrs[1]
		}
		return RelayerSubstrateChainConfig{
			Type:             chainType,
			Name:             chainConfig.Name,
			ParaID:           2000,
			ParachainRPCURL:  strings.Replace(strings.Replace(paraRPCAddr, "http", "ws", 1), "9933", "27451", 1),
			RelayChainRPCURL: strings.Replace(strings.Replace(relayRPCAddr, "http", "ws", 1), "9933", "27451", 1),
			CommitmentPrefix: "0x6962632f",
			PrivateKey:       "//Alice",
			SS58Version:      polkadot.Ss58Format,
			KeyType:          "sr25519",
			FinalityProtocol: "Grandpa",
		}
	} else if chainType == "cosmos" {
		wsURL := strings.Replace(rpcAddr, "http", "ws", 1) + "/websocket"
		return RelayerCosmosChainConfig{
			Type:          chainType,
			Name:          chainConfig.Name,
			ChainID:       chainConfig.ChainID,
			AccountPrefix: chainConfig.Bech32Prefix,
			GRPCUrl:       "http://" + grpcAddr,
			RPCUrl:        rpcAddr,
			StorePrefix:   "ibc",
			MaxTxSize:     200000,
			WebsocketURL:  wsURL,
		}
	} else {
		panic(fmt.Sprintf("unsupported chain type %s", chainType))
	}
}