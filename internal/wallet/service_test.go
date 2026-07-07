// Package wallet_test verifies custody wallet orchestration behavior.
package wallet_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rvmz/mpc-custody/internal/chains/bitcoin"
	"github.com/rvmz/mpc-custody/internal/chains/evm"
	"github.com/rvmz/mpc-custody/internal/observability"
	"github.com/rvmz/mpc-custody/internal/signing"
	"github.com/rvmz/mpc-custody/internal/store"
	"github.com/rvmz/mpc-custody/internal/wallet"
)

func TestEVMProposalRequiresTwoApprovalsBeforeBroadcast(t *testing.T) {
	service := newTestService()
	ctx := context.Background()

	created, err := service.CreateWallet(ctx, wallet.ChainEVM)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	proposal, err := service.ProposeTransaction(ctx, wallet.TransactionRequest{
		WalletID:     created.ID,
		To:           "0x1111111111111111111111111111111111111111",
		Amount:       "1000000000000000",
		GasLimit:     21000,
		MaxFeePerGas: "2000000000",
	})
	if err != nil {
		t.Fatalf("propose transaction: %v", err)
	}

	proposal, err = service.CoSign(ctx, proposal.ID, "alice")
	if err != nil {
		t.Fatalf("first cosign: %v", err)
	}
	if proposal.Status != wallet.TransactionStatusProposed {
		t.Fatalf("status after one approval = %s, want %s", proposal.Status, wallet.TransactionStatusProposed)
	}

	proposal, err = service.CoSign(ctx, proposal.ID, "bob")
	if err != nil {
		t.Fatalf("second cosign: %v", err)
	}
	if proposal.Status != wallet.TransactionStatusSigned {
		t.Fatalf("status after quorum = %s, want %s", proposal.Status, wallet.TransactionStatusSigned)
	}
	if proposal.SignedTransaction == "" {
		t.Fatal("signed transaction is empty")
	}
	if !strings.HasPrefix(proposal.SignedTransaction, "0x") {
		t.Fatalf("signed EVM transaction = %q, want raw hex transaction", proposal.SignedTransaction)
	}

	proposal, err = service.Broadcast(ctx, proposal.ID)
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if proposal.Status != wallet.TransactionStatusBroadcast {
		t.Fatalf("status after broadcast = %s, want %s", proposal.Status, wallet.TransactionStatusBroadcast)
	}
}

func TestBitcoinProposalRequiresUTXOInputs(t *testing.T) {
	service := newTestService()
	ctx := context.Background()

	created, err := service.CreateWallet(ctx, wallet.ChainBitcoin)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	_, err = service.ProposeTransaction(ctx, wallet.TransactionRequest{
		WalletID:    created.ID,
		To:          "tb1qrecipient",
		Amount:      "50000",
		FeeRateSats: 5,
	})
	if err == nil {
		t.Fatal("expected missing UTXO error")
	}
}

func TestDuplicateApprovalIsRejected(t *testing.T) {
	service := newTestService()
	ctx := context.Background()

	created, err := service.CreateWallet(ctx, wallet.ChainEVM)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	proposal, err := service.ProposeTransaction(ctx, wallet.TransactionRequest{
		WalletID:     created.ID,
		To:           "0x1111111111111111111111111111111111111111",
		Amount:       "1",
		GasLimit:     21000,
		MaxFeePerGas: "1",
	})
	if err != nil {
		t.Fatalf("propose transaction: %v", err)
	}

	if _, err := service.CoSign(ctx, proposal.ID, "alice"); err != nil {
		t.Fatalf("first cosign: %v", err)
	}
	if _, err := service.CoSign(ctx, proposal.ID, "alice"); err != wallet.ErrDuplicateApproval {
		t.Fatalf("duplicate approval error = %v, want %v", err, wallet.ErrDuplicateApproval)
	}
}

func newTestService() *wallet.Service {
	registry := wallet.NewChainRegistry(
		bitcoin.NewAdapter("testnet"),
		evm.NewAdapter(31337),
	)
	signer, err := signing.NewDemoQuorumBackend()
	if err != nil {
		panic(err)
	}
	return wallet.NewService(
		store.NewMemoryStore(),
		registry,
		signer,
		observability.NewMetrics(),
	)
}
