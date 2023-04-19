package cosmos

import (
	"context"
)

func FeeabsCrossChainSwap(c *CosmosChain, ctx context.Context, keyName string, ibcDenom string) error {
	tn := c.getFullNode()

	_, err := tn.ExecTx(ctx, keyName,
		"swap", ibcDenom,
		"--pool-file", "--gas", "auto",
	)

	return err
}
