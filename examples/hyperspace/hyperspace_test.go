package hyperspace_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
	"encoding/json"

	"github.com/icza/dyno"
	transfertypes "github.com/cosmos/ibc-go/v7/modules/apps/transfer/types"
	"github.com/strangelove-ventures/interchaintest/v7"
	"github.com/strangelove-ventures/interchaintest/v7/chain/cosmos"
	"github.com/strangelove-ventures/interchaintest/v7/chain/polkadot"
	"github.com/strangelove-ventures/interchaintest/v7/ibc"
	"github.com/strangelove-ventures/interchaintest/v7/relayer"
	"github.com/strangelove-ventures/interchaintest/v7/testreporter"
	"github.com/strangelove-ventures/interchaintest/v7/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestHyperspace setup
// Must build local docker images of hyperspace, parachain, and polkadot
// ###### hyperspace ######
// * Repo: ComposableFi/centauri
// * Branch: vmarkushin/wasm
// * Commit: 64f26da7a4fa3a301c5c147df363c5b5aef83c7d
// * Build local Hyperspace docker from centauri repo:
//    amd64: "docker build -f scripts/hyperspace.Dockerfile -t hyperspace:local ."
//    arm64: "docker build -f scripts/hyperspace.aarch64.Dockerfile -t hyperspace:latest --platform=linux/arm64/v8 .
// ###### parachain ######
// * Repo: ComposableFi/centauri
// * Branch: vmarkushin/wasm
// * Commit: 64f26da7a4fa3a301c5c147df363c5b5aef83c7d
// * Build local parachain docker from centauri repo:
//     ./scripts/build-parachain-node-docker.sh (you can change the script to compile for ARM arch if needed)
// ###### polkadot ######
// * Repo: paritytech/polkadot
// * Branch: release-v0.9.36
// * Commit: dc25abc712e42b9b51d87ad1168e453a42b5f0bc
// * Build local polkadot docker from  polkadot repo
//     amd64: docker build -f scripts/ci/dockerfiles/polkadot/polkadot_builder.Dockerfile . -t polkadot-node:local
//     arm64: docker build --platform linux/arm64 -f scripts/ci/dockerfiles/polkadot/polkadot_builder.aarch64.Dockerfile . -t polkadot-node:local

const (
	heightDelta    = uint64(20)
	votingPeriod       = "30s"
	maxDepositPeriod   = "10s"
)

// TestHyperspace features
// * sets up a Polkadot parachain
// * sets up a Cosmos chain
// * sets up the Hyperspace relayer
// * Funds a user wallet on both chains
// * Pushes a wasm client contract to the Cosmos chain
// * create client, connection, and channel in relayer
// * start relayer
// * send transfer over ibc
func TestHyperspace(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	t.Parallel()

	client, network := interchaintest.DockerSetup(t)

	rep := testreporter.NewNopReporter()
	eRep := rep.RelayerExecReporter(t)

	ctx := context.Background()

	nv := 5 // Number of validators
	nf := 3 // Number of full nodes

	// Override config files to support an ~2.5MB contract
	configFileOverrides := make(map[string]any)

	appTomlOverrides := make(testutil.Toml)
	configTomlOverrides := make(testutil.Toml)

	apiOverrides := make(testutil.Toml)
	apiOverrides["rpc-max-body-bytes"] = 1_800_000
	appTomlOverrides["api"] = apiOverrides

	rpcOverrides := make(testutil.Toml)
	rpcOverrides["max_body_bytes"] = 1_800_000
	rpcOverrides["max_header_bytes"] = 1_900_000
	
	consensusOverrides := make(testutil.Toml)
	blockTime   := 5 // seconds, parachain is 12 second blocks, don't make relayer work harder than needed
	blockT := (time.Duration(blockTime) * time.Second).String()
	consensusOverrides["timeout_commit"] = blockT
	consensusOverrides["timeout_propose"] = blockT

	configTomlOverrides["rpc"] = rpcOverrides
	configTomlOverrides["consensus"] = consensusOverrides

	//mempoolOverrides := make(testutil.Toml)
	//mempoolOverrides["max_tx_bytes"] = 6000000
	//configTomlOverrides["mempool"] = mempoolOverrides

	configFileOverrides["config/app.toml"] = appTomlOverrides
	configFileOverrides["config/config.toml"] = configTomlOverrides

	// Get both chains
	cf := interchaintest.NewBuiltinChainFactory(zaptest.NewLogger(t), []*interchaintest.ChainSpec{
		{
			ChainName: "composable", // Set ChainName so that a suffix with a "dash" is not appended (required for hyperspace)
			ChainConfig: ibc.ChainConfig{
				Type:    "polkadot",
				Name:    "composable",
				ChainID: "rococo-local",
				Images: []ibc.DockerImage{
					{
						Repository: "polkadot-node",
						Version:    "local",
						UidGid:     "1025:1025",
					},
					{
						Repository: "parachain-node",
						Version:    "latest",
						//UidGid: "1025:1025",
					},
				},
				Bin:            "polkadot",
				Bech32Prefix:   "composable",
				Denom:          "uDOT",
				GasPrices:      "",
				GasAdjustment:  0,
				TrustingPeriod: "",
				CoinType:       "354",
			},
			NumValidators: &nv,
			NumFullNodes:  &nf,
		},
		{
			ChainName: "simd", // Set chain name so that a suffix with a "dash" is not appended (required for hyperspace)
			ChainConfig: ibc.ChainConfig{
				Type:    "cosmos",
				Name:    "simd",
				ChainID: "simd",
				Images: []ibc.DockerImage{
					{
						Repository: "ghcr.io/strangelove-ventures/heighliner/ibc-go-simd",
						Version:    "feat-wasm-clients",
						UidGid:     "1025:1025",
					},
				},
				Bin:            "simd",
				Bech32Prefix:   "cosmos",
				Denom:          "stake",
				GasPrices:      "0.00stake",
				GasAdjustment:  1.3,
				TrustingPeriod: "504h",
				CoinType:       "118",
				//EncodingConfig: WasmClientEncoding(),
				NoHostMount:         true,
				ConfigFileOverrides: configFileOverrides,
				ModifyGenesis: modifyGenesisShortProposals(votingPeriod, maxDepositPeriod),
			},
		},
	})

	chains, err := cf.Chains(t.Name())
	require.NoError(t, err)

	polkadotChain := chains[0].(*polkadot.PolkadotChain)
	cosmosChain := chains[1].(*cosmos.CosmosChain)

	// Get a relayer instance
	r := interchaintest.NewBuiltinRelayerFactory(
		ibc.Hyperspace,
		zaptest.NewLogger(t),
		// These two fields are used to pass in a custom Docker image built locally
		relayer.ImagePull(false),
		relayer.CustomDockerImage("hyperspace", "local", "1000:1000"),
	).Build(t, client, network)

	// Build the network; spin up the chains and configure the relayer
	const pathName = "composable-simd"
	const relayerName = "hyperspace"

	ic := interchaintest.NewInterchain().
		AddChain(polkadotChain).
		AddChain(cosmosChain).
		AddRelayer(r, relayerName).
		AddLink(interchaintest.InterchainLink{
			Chain1:  polkadotChain,
			Chain2:  cosmosChain,
			Relayer: r,
			Path:    pathName,
		})

	require.NoError(t, ic.Build(ctx, eRep, interchaintest.InterchainBuildOptions{
		TestName:          t.Name(),
		Client:            client,
		NetworkID:         network,
		BlockDatabaseFile: interchaintest.DefaultBlockDatabaseFilepath(),
		SkipPathCreation:  true, // Skip path creation, so we can have granular control over the process
	}))
	fmt.Println("Interchain built")

	t.Cleanup(func() {
		_ = ic.Close()
	})

	// Create a proposal, vote, and wait for it to pass. Return code hash for relayer.
	codeHash := pushWasmContractViaGov(t, ctx, cosmosChain)

	// Set client contract hash in cosmos chain config
	err = r.SetClientContractHash(ctx, eRep, cosmosChain.Config(), codeHash)
	require.NoError(t, err)

	// Ensure parachain has started (starts 1 session/epoch after relay chain)
	err = testutil.WaitForBlocks(ctx, 1, polkadotChain)
	require.NoError(t, err, "polkadot chain failed to make blocks")

	// Enable IBC transfers on parachain
	err = polkadotChain.EnableIbcTransfers()
	require.NoError(t, err)
	
	// Fund users on both cosmos and parachain, mints Asset 1 for Alice
	fundAmount := int64(12_333_000_000_000)
	polkadotUser, cosmosUser := fundUsers(t, ctx, fundAmount, polkadotChain, cosmosChain)

	err = r.GeneratePath(ctx, eRep, cosmosChain.Config().ChainID, polkadotChain.Config().ChainID, pathName)
	require.NoError(t, err)

	// Create new clients
	err = r.CreateClients(ctx, eRep, pathName, ibc.DefaultClientOpts())
	require.NoError(t, err)
	err = testutil.WaitForBlocks(ctx, 1, cosmosChain, polkadotChain) // these 1 block waits may be needed, not sure
	require.NoError(t, err)

	// Create a new connection
	err = r.CreateConnections(ctx, eRep, pathName)
	require.NoError(t, err)
	err = testutil.WaitForBlocks(ctx, 1, cosmosChain, polkadotChain)
	require.NoError(t, err)

	// Create a new channel & get channels from each chain
	err = r.CreateChannel(ctx, eRep, pathName, ibc.DefaultChannelOpts())
	require.NoError(t, err)
	err = testutil.WaitForBlocks(ctx, 1, cosmosChain, polkadotChain)
	require.NoError(t, err)

	// Get channels - Query channels was removed
	/*cosmosChannelOutput, err := r.GetChannels(ctx, eRep, cosmosChain.Config().ChainID)
	require.NoError(t, err)
	require.Equal(t, len(cosmosChannelOutput), 1)
	require.Equal(t, cosmosChannelOutput[0].ChannelID, "channel-0")
	require.Equal(t, cosmosChannelOutput[0].PortID, "transfer")

	polkadotChannelOutput, err := r.GetChannels(ctx, eRep, polkadotChain.Config().ChainID)
	require.NoError(t, err)
	require.Equal(t, len(polkadotChannelOutput), 1)
	require.Equal(t, polkadotChannelOutput[0].ChannelID, "channel-0")
	require.Equal(t, polkadotChannelOutput[0].PortID, "transfer")*/

	// Start relayer
	r.StartRelayer(ctx, eRep, pathName)
	require.NoError(t, err)
	t.Cleanup(func() {
		err = r.StopRelayer(ctx, eRep)
		if err != nil {
			panic(err)
		}
	})

	// Send 1.77 stake from cosmosUser to parachainUser
	amountToSend := int64(1_770_000)
	transfer := ibc.WalletAmount{
		Address: polkadotUser.FormattedAddress(),
		Denom:   cosmosChain.Config().Denom,
		Amount:  amountToSend,
	}
	tx, err := cosmosChain.SendIBCTransfer(ctx, "channel-0", cosmosUser.KeyName(), transfer, ibc.TransferOptions{})
	require.NoError(t, err)
	require.NoError(t, tx.Validate()) // test source wallet has decreased funds
	err = testutil.WaitForBlocks(ctx, 5, cosmosChain, polkadotChain)
	require.NoError(t, err)

	/*// Trace IBC Denom of stake on parachain
	srcDenomTrace := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(cosmosChannelOutput[0].PortID, cosmosChannelOutput[0].ChannelID, cosmosChain.Config().Denom))
	dstIbcDenom := srcDenomTrace.IBCDenom()
	fmt.Println("Dst Ibc denom: ", dstIbcDenom)

	// Test destination wallet has increased funds, this is not working, want to verify IBC balance on parachain
	polkadotUserIbcCoins, err := polkadotChain.GetIbcBalance(ctx, string(polkadotUser.Address()))
	fmt.Println("UserIbcCoins: ", polkadotUserIbcCoins.String())
	aliceIbcCoins, err := polkadotChain.GetIbcBalance(ctx, "5yNZjX24n2eg7W6EVamaTXNQbWCwchhThEaSWB7V3GRjtHeL")
	fmt.Println("AliceIbcCoins: ", aliceIbcCoins.String())*/

	// Send 1.16 stake from parachainUser to cosmosUser
	amountToReflect := int64(1_160_000)
	reflectTransfer := ibc.WalletAmount{
		Address: cosmosUser.FormattedAddress(),
		Denom:   "2", // stake
		Amount:  amountToReflect,
	}
	_, err = polkadotChain.SendIBCTransfer(ctx, "channel-0", polkadotUser.KeyName(), reflectTransfer, ibc.TransferOptions{})
	require.NoError(t, err)

	// Send 1.88 "UNIT" from Alice to cosmosUser
	amountUnits := int64(1_880_000_000_000)
	unitTransfer := ibc.WalletAmount{
		Address: cosmosUser.FormattedAddress(),
		Denom:   "1", // UNIT
		Amount:  amountUnits,
	}
	_, err = polkadotChain.SendIBCTransfer(ctx, "channel-0", "alice", unitTransfer, ibc.TransferOptions{})
	require.NoError(t, err)

	// Wait for MsgRecvPacket on cosmos chain
	finalStakeBal := fundAmount - amountToSend + amountToReflect
	err = cosmos.PollForBalance(ctx, cosmosChain, 20, ibc.WalletAmount{
		Address: cosmosUser.FormattedAddress(),
		Denom:   cosmosChain.Config().Denom,
		Amount:  finalStakeBal,
	})
	require.NoError(t, err)

	// Verify final cosmos user "stake" balance
	cosmosUserStakeBal, err := cosmosChain.GetBalance(ctx, cosmosUser.FormattedAddress(), cosmosChain.Config().Denom)
	require.NoError(t, err)
	require.Equal(t, finalStakeBal, cosmosUserStakeBal)
	// Verify final cosmos user "unit" balance
	unitDenomTrace := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom("transfer", "channel-0", "UNIT"))
	cosmosUserUnitBal, err := cosmosChain.GetBalance(ctx, cosmosUser.FormattedAddress(), unitDenomTrace.IBCDenom())
	require.NoError(t, err)
	require.Equal(t, amountUnits, cosmosUserUnitBal)
	/*polkadotUserIbcCoins, err = polkadotChain.GetIbcBalance(ctx, string(polkadotUser.Address()))
	fmt.Println("UserIbcCoins: ", polkadotUserIbcCoins.String())
	aliceIbcCoins, err = polkadotChain.GetIbcBalance(ctx, "5yNZjX24n2eg7W6EVamaTXNQbWCwchhThEaSWB7V3GRjtHeL")
	fmt.Println("AliceIbcCoins: ", aliceIbcCoins.String())*/

	fmt.Println("********************************")
	fmt.Println("********* Test passed **********")
	fmt.Println("********************************")

	//err = testutil.WaitForBlocks(ctx, 50, cosmosChain, polkadotChain)
	//require.NoError(t, err)
}

type GetCodeQueryMsgResponse struct {
	Code []byte `json:"code"`
}

func pushWasmContractViaGov(t *testing.T, ctx context.Context, cosmosChain *cosmos.CosmosChain) string {
	// Set up cosmos user for pushing new wasm code msg via governance
	fundAmountForGov := int64(10_000_000_000)
	contractUsers := interchaintest.GetAndFundTestUsers(t, ctx, "default", int64(fundAmountForGov), cosmosChain)
	contractUser := contractUsers[0]

	contractUserBalInitial, err := cosmosChain.GetBalance(ctx, contractUser.FormattedAddress(), cosmosChain.Config().Denom)
	require.NoError(t, err)
	require.Equal(t, fundAmountForGov, contractUserBalInitial)

	proposal := cosmos.TxProposalv1{
		Metadata: "none",
		Deposit: "500000000" + cosmosChain.Config().Denom, // greater than min deposit
		Title: "Grandpa Contract",
		Summary: "new grandpa contract",
	}

	proposalTx, codeHash, err := cosmosChain.PushNewWasmClientProposal(ctx, contractUser.KeyName(), "../polkadot/ics10_grandpa_cw.wasm", proposal)
	require.NoError(t, err, "error submitting new wasm contract proposal tx")

	height, err := cosmosChain.Height(ctx)
	require.NoError(t, err, "error fetching height before submit upgrade proposal")
	
	err = cosmosChain.VoteOnProposalAllValidators(ctx, proposalTx.ProposalID, cosmos.ProposalVoteYes)
	require.NoError(t, err, "failed to submit votes")

	_, err = cosmos.PollForProposalStatus(ctx, cosmosChain, height, height+heightDelta, proposalTx.ProposalID, cosmos.ProposalStatusPassed)
	require.NoError(t, err, "proposal status did not change to passed in expected number of blocks")

	err = testutil.WaitForBlocks(ctx, 1, cosmosChain)
	require.NoError(t, err)

	var getCodeQueryMsgRsp GetCodeQueryMsgResponse
	err = cosmosChain.QueryClientContractCode(ctx, codeHash, &getCodeQueryMsgRsp)
	codeHashByte32 := sha256.Sum256(getCodeQueryMsgRsp.Code)
	codeHash2 := hex.EncodeToString(codeHashByte32[:])
	t.Logf("Contract codeHash from code: %s", codeHash2)
	require.NoError(t, err)
	require.NotEmpty(t, getCodeQueryMsgRsp.Code)
	require.Equal(t, codeHash, codeHash2)

	return codeHash
}

func fundUsers(t *testing.T, ctx context.Context, fundAmount int64, polkadotChain ibc.Chain, cosmosChain ibc.Chain)(ibc.Wallet, ibc.Wallet) {
	users := interchaintest.GetAndFundTestUsers(t, ctx, "user", fundAmount, polkadotChain, cosmosChain)
	polkadotUser, cosmosUser := users[0], users[1]
	err := testutil.WaitForBlocks(ctx, 2, polkadotChain, cosmosChain) // Only waiting 1 block is flaky for parachain
	require.NoError(t, err, "cosmos or polkadot chain failed to make blocks")

	// Check balances are correct
	polkadotUserAmount, err := polkadotChain.GetBalance(ctx, polkadotUser.FormattedAddress(), polkadotChain.Config().Denom)
	require.NoError(t, err)
	require.Equal(t, fundAmount, polkadotUserAmount, "Initial polkadot user amount not expected")
	parachainUserAmount, err := polkadotChain.GetBalance(ctx, polkadotUser.FormattedAddress(), "")
	require.NoError(t, err)
	require.Equal(t, fundAmount, parachainUserAmount, "Initial parachain user amount not expected")
	cosmosUserAmount, err := cosmosChain.GetBalance(ctx, cosmosUser.FormattedAddress(), cosmosChain.Config().Denom)
	require.NoError(t, err)
	require.Equal(t, fundAmount, cosmosUserAmount, "Initial cosmos user amount not expected")
	
	// Mint 100 "UNIT"/"Asset 1" for alice , not sure why the ~1.5M UNIT from balance/genesis doesn't work
	mint := ibc.WalletAmount{
		Address: "5yNZjX24n2eg7W6EVamaTXNQbWCwchhThEaSWB7V3GRjtHeL",
		Denom:   "1",
		Amount:  int64(100_000_000_000_000), // 100 UNITS, not 100T
	}
	err = polkadotChain.(*polkadot.PolkadotChain).MintFunds("alice", mint)
	require.NoError(t, err)
	err = testutil.WaitForBlocks(ctx, 2, polkadotChain, cosmosChain) // Only waiting 1 block is flaky for parachain
	require.NoError(t, err, "cosmos or polkadot chain failed to make blocks")
	// Mint 100 "UNIT"/"Asset 1" for alice , not sure why the ~1.5M UNIT from balance/genesis doesn't work
	mint2 := ibc.WalletAmount{
		Address: polkadotUser.FormattedAddress(), // Alice
		Denom:   "1",
		Amount:  int64(123_789_000_000_000), // 100 UNITS, not 100T
	}
	err = polkadotChain.(*polkadot.PolkadotChain).MintFunds("alice", mint2)
	require.NoError(t, err)

	return polkadotUser, cosmosUser
}

func modifyGenesisShortProposals(votingPeriod string, maxDepositPeriod string) func(ibc.ChainConfig, []byte) ([]byte, error) {
	return func(chainConfig ibc.ChainConfig, genbz []byte) ([]byte, error) {
		g := make(map[string]interface{})
		if err := json.Unmarshal(genbz, &g); err != nil {
			return nil, fmt.Errorf("failed to unmarshal genesis file: %w", err)
		}
		if err := dyno.Set(g, votingPeriod, "app_state", "gov", "params", "voting_period"); err != nil {
			return nil, fmt.Errorf("failed to set voting period in genesis json: %w", err)
		}
		if err := dyno.Set(g, maxDepositPeriod, "app_state", "gov", "params", "max_deposit_period"); err != nil {
			return nil, fmt.Errorf("failed to set max deposit period in genesis json: %w", err)
		}
		if err := dyno.Set(g, chainConfig.Denom, "app_state", "gov", "params", "min_deposit", 0, "denom"); err != nil {
			return nil, fmt.Errorf("failed to set min deposit in genesis json: %w", err)
		}
		out, err := json.Marshal(g)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal genesis bytes to json: %w", err)
		}
		return out, nil
	}
}