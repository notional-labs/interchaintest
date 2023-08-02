package cosmos

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/strangelove-ventures/interchaintest/v7/chain/internal/tendermint"
	"github.com/strangelove-ventures/interchaintest/v7/ibc"
	"github.com/strangelove-ventures/interchaintest/v7/internal/dockerutil"
	"github.com/strangelove-ventures/interchaintest/v7/testutil"
)

type HostChainFeeAbsConfig struct {
	IbcDenom                string `protobuf:"bytes,1,opt,name=ibc_denom,json=ibcDenom,proto3" json:"ibc_denom,omitempty" yaml:"allowed_token"`
	OsmosisPoolTokenDenomIn string `protobuf:"bytes,2,opt,name=osmosis_pool_token_denom_in,json=osmosisPoolTokenDenomIn,proto3" json:"osmosis_pool_token_denom_in,omitempty"`
	PoolId                  uint64 `protobuf:"varint,3,opt,name=pool_id,json=poolId,proto3" json:"pool_id,omitempty"`
	Frozen                  bool   `protobuf:"varint,4,opt,name=frozen,proto3" json:"frozen,omitempty"`
}

type AddHostZoneProposal struct {
	Title           string                `protobuf:"bytes,1,opt,name=title,proto3" json:"title,omitempty"`
	Description     string                `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	HostChainConfig HostChainFeeAbsConfig `protobuf:"bytes,3,opt,name=host_chain_config,json=hostChainConfig,proto3" json:"host_chain_config,omitempty"`
	Deposit         string                `json:"deposit"`
}

func FeeabsCrossChainSwap(c *CosmosChain, ctx context.Context, keyName string, ibcDenom string) (tx ibc.Tx, _ error) {
	tn := c.getFullNode()

	txHash, _ := tn.ExecTx(ctx, keyName,
		"feeabs", "swap", ibcDenom,
		"--gas", "auto",
	)

	if err := testutil.WaitForBlocks(ctx, 2, tn); err != nil {
		return tx, err
	}

	txResp, err := c.getTransaction(txHash)
	if err != nil {
		return tx, fmt.Errorf("failed to get transaction %s: %w", txHash, err)
	}
	tx.Height = uint64(txResp.Height)
	tx.TxHash = txHash

	tx.GasSpent = txResp.GasWanted

	const evType = "send_packet"
	events := txResp.Events

	var (
		seq, _           = tendermint.AttributeValue(events, evType, "packet_sequence")
		srcPort, _       = tendermint.AttributeValue(events, evType, "packet_src_port")
		srcChan, _       = tendermint.AttributeValue(events, evType, "packet_src_channel")
		dstPort, _       = tendermint.AttributeValue(events, evType, "packet_dst_port")
		dstChan, _       = tendermint.AttributeValue(events, evType, "packet_dst_channel")
		timeoutHeight, _ = tendermint.AttributeValue(events, evType, "packet_timeout_height")
		timeoutTs, _     = tendermint.AttributeValue(events, evType, "packet_timeout_timestamp")
		data, _          = tendermint.AttributeValue(events, evType, "packet_data")
	)

	tx.Packet.SourcePort = srcPort
	tx.Packet.SourceChannel = srcChan
	tx.Packet.DestPort = dstPort
	tx.Packet.DestChannel = dstChan
	tx.Packet.TimeoutHeight = timeoutHeight
	tx.Packet.Data = []byte(data)

	seqNum, err := strconv.Atoi(seq)
	if err != nil {
		return tx, fmt.Errorf("invalid packet sequence from events %s: %w", seq, err)
	}
	tx.Packet.Sequence = uint64(seqNum)

	timeoutNano, err := strconv.ParseUint(timeoutTs, 10, 64)
	if err != nil {
		return tx, fmt.Errorf("invalid packet timestamp timeout %s: %w", timeoutTs, err)
	}
	tx.Packet.TimeoutTimestamp = ibc.Nanoseconds(timeoutNano)

	return tx, err
}

func FeeabsAddHostZoneProposal(c *CosmosChain, ctx context.Context, keyName string, fileLocation string) (string, error) {
	tn := c.getFullNode()
	dat, err := os.ReadFile(fileLocation)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	fileName := "add-hostzone.json"

	fw := dockerutil.NewFileWriter(tn.logger(), tn.DockerClient, tn.TestName)
	if err := fw.WriteFile(ctx, tn.VolumeName, fileName, dat); err != nil {
		return "", fmt.Errorf("failure writing proposal json: %w", err)
	}

	filePath := filepath.Join(tn.HomeDir(), fileName)

	command := []string{
		"gov", "submit-legacy-proposal",
		"add-hostzone-config", filePath,
	}
	return tn.ExecTx(ctx, keyName, command...)
}
