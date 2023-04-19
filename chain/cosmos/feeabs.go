package cosmos

import (
	"context"
	"fmt"
)

func FeeabsCrossChainSwap(c *CosmosChain, ctx context.Context, keyName string, ibcDenom string) error {
	tn := c.getFullNode()

	if _, err := tn.ExecTx(ctx, keyName,
		"swap", ibcDenom,
		"--pool-file", "--gas", "auto",
	); err != nil {
		return fmt.Errorf("failed to swap: %w", err)
	}
}
