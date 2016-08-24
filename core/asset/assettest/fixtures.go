package assettest

import (
	"context"
	"testing"
	"time"

	"chain/core/account"
	"chain/core/asset"
	"chain/core/blocksigner"
	"chain/core/generator"
	"chain/core/mockhsm"
	"chain/core/txbuilder"
	"chain/database/pg"
	"chain/encoding/json"
	"chain/errors"
	"chain/protocol"
	"chain/protocol/bc"
	"chain/protocol/mempool"
	"chain/protocol/memstore"
	"chain/protocol/state"
	"chain/testutil"
)

func CreateAccountFixture(ctx context.Context, t testing.TB, keys []string, quorum int, alias string, tags map[string]interface{}) string {
	if keys == nil {
		keys = []string{testutil.TestXPub.String()}
	}
	if quorum == 0 {
		quorum = len(keys)
	}
	acc, err := account.Create(ctx, keys, quorum, alias, tags, nil)
	if err != nil {
		testutil.FatalErr(t, err)
	}
	return acc.ID
}

func CreateAccountControlProgramFixture(ctx context.Context, t testing.TB, accID string) []byte {
	if accID == "" {
		accID = CreateAccountFixture(ctx, t, nil, 0, "", nil)
	}
	controlProgram, err := account.CreateControlProgram(ctx, accID)
	if err != nil {
		testutil.FatalErr(t, err)
	}
	return controlProgram
}

func CreateAssetFixture(ctx context.Context, t testing.TB, keys []string, quorum int, def map[string]interface{}, alias string, tags map[string]interface{}) bc.AssetID {
	if len(keys) == 0 {
		keys = []string{testutil.TestXPub.String()}
	}

	if quorum == 0 {
		quorum = len(keys)
	}
	var genesisHash bc.Hash

	asset, err := asset.Define(ctx, keys, quorum, def, genesisHash, alias, tags, nil)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	return asset.AssetID
}

func IssueAssetsFixture(ctx context.Context, t testing.TB, fc *protocol.FC, assetID bc.AssetID, amount uint64, accountID string) state.Output {
	if accountID == "" {
		accountID = CreateAccountFixture(ctx, t, nil, 0, "", nil)
	}
	dest := NewAccountControlAction(bc.AssetAmount{AssetID: assetID, Amount: amount}, accountID, nil)

	assetAmount := bc.AssetAmount{AssetID: assetID, Amount: amount}

	src := NewIssueAction(assetAmount, nil) // does not support reference data
	tpl, err := txbuilder.Build(ctx, nil, []txbuilder.Action{dest, src}, nil)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	SignTxTemplate(t, tpl, testutil.TestXPrv)

	tx, err := txbuilder.FinalizeTx(ctx, fc, tpl)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	return state.Output{
		Outpoint: bc.Outpoint{Hash: tx.Hash, Index: 0},
		TxOutput: *tx.Outputs[0],
	}
}

// InitializeSigningGenerator initiaizes a generator fixture with the
// provided store. Store can be nil, in which case it will use memstore.
func InitializeSigningGenerator(ctx context.Context, store protocol.Store, pool protocol.Pool) (*protocol.FC, *generator.Generator, error) {
	if store == nil {
		store = memstore.New()
	}
	if pool == nil {
		pool = mempool.New()
	}
	fc, err := protocol.NewFC(ctx, store, pool, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	asset.Init(fc, nil, true)
	account.Init(fc, nil)

	hsm := mockhsm.New(pg.FromContext(ctx))
	xpub, err := hsm.CreateKey(ctx, "")
	if err != nil {
		return nil, nil, err
	}

	localSigner := blocksigner.New(xpub.XPub, hsm, pg.FromContext(ctx), fc)
	config := generator.Config{
		LocalSigner: localSigner,
		FC:          fc,
	}
	b1, err := protocol.NewGenesisBlock(nil, 0, time.Now())
	if err != nil {
		return nil, nil, err
	}
	err = fc.CommitBlock(ctx, b1, state.Empty())
	if err != nil {
		return nil, nil, err
	}
	g := generator.New(b1, state.Empty(), config)
	return fc, g, nil
}

func Issue(ctx context.Context, t testing.TB, fc *protocol.FC, assetID bc.AssetID, amount uint64, actions []txbuilder.Action) *bc.Tx {
	assetAmount := bc.AssetAmount{AssetID: assetID, Amount: amount}
	actions = append(actions, NewIssueAction(assetAmount, nil))

	txTemplate, err := txbuilder.Build(
		ctx,
		nil,
		actions,
		nil,
	)
	if err != nil {
		t.Log(errors.Stack(err))
		t.Fatal(err)
	}
	SignTxTemplate(t, txTemplate, nil)
	tx, err := txbuilder.FinalizeTx(ctx, fc, txTemplate)
	if err != nil {
		t.Log(errors.Stack(err))
		t.Fatal(err)
	}

	return tx
}

func Transfer(ctx context.Context, t testing.TB, fc *protocol.FC, actions []txbuilder.Action) *bc.Tx {
	template, err := txbuilder.Build(ctx, nil, actions, nil)
	if err != nil {
		t.Log(errors.Stack(err))
		t.Fatal(err)
	}

	SignTxTemplate(t, template, testutil.TestXPrv)

	tx, err := txbuilder.FinalizeTx(ctx, fc, template)
	if err != nil {
		t.Log(errors.Stack(err))
		t.Fatal(err)
	}

	return tx
}

func NewIssueAction(assetAmount bc.AssetAmount, referenceData json.Map) *asset.IssueAction {
	return &asset.IssueAction{
		Params: struct {
			bc.AssetAmount
			TTL     time.Duration
			MinTime *time.Time `json:"min_time"`
		}{assetAmount, 0, nil},
		ReferenceData: referenceData,
	}
}

func NewAccountSpendAction(amt bc.AssetAmount, accountID string, txHash *bc.Hash, txOut *uint32, refData json.Map) *account.SpendAction {
	return &account.SpendAction{
		Params: struct {
			bc.AssetAmount
			AccountID string        `json:"account_id"`
			TxHash    *bc.Hash      `json:"transaction_id"`
			TxOut     *uint32       `json:"position"`
			TTL       time.Duration `json:"reservation_ttl"`
		}{
			AssetAmount: amt,
			AccountID:   accountID,
			TxHash:      txHash,
			TxOut:       txOut,
		},
		ReferenceData: refData,
	}
}

func NewAccountControlAction(amt bc.AssetAmount, accountID string, refData json.Map) *account.ControlAction {
	return &account.ControlAction{
		Params: struct {
			bc.AssetAmount
			AccountID string `json:"account_id"`
		}{amt, accountID},
		ReferenceData: refData,
	}
}
