package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"

	sdk "github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/protocols/horizon/effects"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/services/horizon/internal/test"
	"github.com/stellar/go/services/horizon/internal/txnbuild"
	"github.com/stellar/go/xdr"
)

func getSimpleAccountCreationSandwich(tt *assert.Assertions) (*keypair.Full, []txnbuild.Operation) {
	// We will create the following operation structure:
	// BeginSponsoringFutureReserves A
	//   CreateAccount A
	// EndSponsoringFutureReserves (with A as a source)

	ops := make([]txnbuild.Operation, 3, 3)
	newAccountPair, err := keypair.Random()
	tt.NoError(err)

	ops[0] = &txnbuild.BeginSponsoringFutureReserves{
		SponsoredID: newAccountPair.Address(),
	}
	ops[1] = &txnbuild.CreateAccount{
		Destination: newAccountPair.Address(),
		Amount:      "1.5", // enough for the account to exist (1) and submit an operation (0.5)
	}
	ops[2] = &txnbuild.EndSponsoringFutureReserves{
		SourceAccount: &txnbuild.SimpleAccount{
			AccountID: newAccountPair.Address(),
		},
	}
	return newAccountPair, ops
}

func TestSimpleSandwichHappyPath(t *testing.T) {
	tt := assert.New(t)
	itest := test.NewIntegrationTest(t, protocol14Config)
	defer itest.Close()
	sponsor := itest.MasterAccount()
	sponsorPair := itest.Master()
	newAccountPair, ops := getSimpleAccountCreationSandwich(tt)

	signers := []*keypair.Full{sponsorPair, newAccountPair}
	txResp, err := itest.SubmitMultiSigOperations(sponsor, signers, ops...)
	tt.NoError(err)

	var txResult xdr.TransactionResult
	err = xdr.SafeUnmarshalBase64(txResp.ResultXdr, &txResult)
	tt.NoError(err)
	tt.Equal(xdr.TransactionResultCodeTxSuccess, txResult.Result.Code)

	response, err := itest.Client().Operations(sdk.OperationRequest{
		Order: "asc",
	})
	opRecords := response.Embedded.Records
	tt.NoError(err)
	tt.Len(opRecords, 3)
	tt.True(opRecords[0].IsTransactionSuccessful())

	// Verify operation details
	tt.Equal(ops[0].(*txnbuild.BeginSponsoringFutureReserves).SponsoredID,
		opRecords[0].(operations.BeginSponsoringFutureReserves).SponsoredID)

	actualCreateAccount := opRecords[1].(operations.CreateAccount)
	tt.Equal(sponsorPair.Address(), actualCreateAccount.Sponsor)

	endSponsoringOp := opRecords[2].(operations.EndSponsoringFutureReserves)
	tt.Equal(sponsorPair.Address(), endSponsoringOp.BeginSponsor)

	// Make sure that the sponsor is an (implicit) participant on the end sponsorship operation

	response, err = itest.Client().Operations(sdk.OperationRequest{
		ForAccount: sponsorPair.Address(),
	})
	tt.NoError(err)

	endSponsorshipPresent := func() bool {
		for _, o := range response.Embedded.Records {
			if o.GetID() == endSponsoringOp.ID {
				return true
			}
		}
		return false
	}
	tt.Condition(endSponsorshipPresent)

	// Check numSponsoring and numSponsored
	account, err := itest.Client().AccountDetail(sdk.AccountRequest{
		AccountID: sponsorPair.Address(),
	})
	tt.NoError(err)
	account.NumSponsoring = 1

	account, err = itest.Client().AccountDetail(sdk.AccountRequest{
		AccountID: newAccountPair.Address(),
	})
	tt.NoError(err)
	account.NumSponsored = 1

	// Check effects of CreateAccount Operation
	eResponse, err := itest.Client().Effects(sdk.EffectRequest{ForOperation: opRecords[1].GetID()})
	tt.NoError(err)
	effectRecords := eResponse.Embedded.Records
	tt.Len(effectRecords, 4)
	tt.IsType(effects.AccountSponsorshipCreated{}, effectRecords[3])
	tt.Equal(sponsorPair.Address(), effectRecords[3].(effects.AccountSponsorshipCreated).Sponsor)
}

func TestSimpleSandwichRevocation(t *testing.T) {
	tt := assert.New(t)
	itest := test.NewIntegrationTest(t, protocol14Config)
	defer itest.Close()
	sponsor := itest.MasterAccount()
	sponsorPair := itest.Master()
	newAccountPair, ops := getSimpleAccountCreationSandwich(tt)

	signers := []*keypair.Full{sponsorPair, newAccountPair}
	txResp, err := itest.SubmitMultiSigOperations(sponsor, signers, ops...)
	tt.NoError(err)

	var txResult xdr.TransactionResult
	err = xdr.SafeUnmarshalBase64(txResp.ResultXdr, &txResult)
	tt.NoError(err)
	tt.Equal(xdr.TransactionResultCodeTxSuccess, txResult.Result.Code)

	// Submit sponsorship revocation in a separate transaction
	accountToRevoke := newAccountPair.Address()
	op := &txnbuild.RevokeSponsorship{
		SponsorshipType: txnbuild.RevokeSponsorshipTypeAccount,
		Account:         &accountToRevoke,
	}
	txResp, err = itest.SubmitOperations(sponsor, sponsorPair, op)
	tt.NoError(err)

	err = xdr.SafeUnmarshalBase64(txResp.ResultXdr, &txResult)
	tt.NoError(err)
	tt.Equal(xdr.TransactionResultCodeTxSuccess, txResult.Result.Code)

	// Verify operation details
	response, err := itest.Client().Operations(sdk.OperationRequest{
		ForTransaction: txResp.Hash,
	})
	opRecords := response.Embedded.Records
	tt.NoError(err)
	tt.Len(opRecords, 1)
	tt.True(opRecords[0].IsTransactionSuccessful())

	revokeOp := opRecords[0].(operations.RevokeSponsorship)
	tt.Equal(*op.Account, *revokeOp.AccountID)

	// Make sure that the sponsoree is an (implicit) participant in the revocation operation
	response, err = itest.Client().Operations(sdk.OperationRequest{
		ForAccount: newAccountPair.Address(),
	})
	tt.NoError(err)

	sponsorshipRevocationPresent := func() bool {
		for _, o := range response.Embedded.Records {
			if o.GetID() == revokeOp.ID {
				return true
			}
		}
		return false
	}
	tt.Condition(sponsorshipRevocationPresent)

	// Check effects
	eResponse, err := itest.Client().Effects(sdk.EffectRequest{ForOperation: revokeOp.ID})
	tt.NoError(err)
	effectRecords := eResponse.Embedded.Records
	tt.Len(effectRecords, 1)
	tt.IsType(effects.AccountSponsorshipRemoved{}, effectRecords[0])
	tt.Equal(sponsorPair.Address(), effectRecords[0].(effects.AccountSponsorshipRemoved).FormerSponsor)
}

func TestSponsorPreAuthSigner(t *testing.T) {
	tt := assert.New(t)
	itest := test.NewIntegrationTest(t, protocol14Config)
	defer itest.Close()
	sponsor := itest.MasterAccount()
	sponsorPair := itest.Master()
	newAccountPair, ops := getSimpleAccountCreationSandwich(tt)

	// Let's create a preauthorized transaction for the new account
	// to add a signer
	preAuthOp := txnbuild.SetOptions{
		Signer: &txnbuild.Signer{
			// unspecific signer
			Address: "GC3C4AKRBQLHOJ45U4XG35ESVWRDECWO5XLDGYADO6DPR3L7KIDVUMML",
			Weight:  1,
		},
	}
	newAccount := txnbuild.SimpleAccount{
		AccountID: newAccountPair.Address(),
		Sequence:  0,
	}
	txParams := txnbuild.TransactionParams{
		SourceAccount:        &newAccount,
		Operations:           []txnbuild.Operation{&preAuthOp},
		BaseFee:              txnbuild.MinBaseFee,
		Timebounds:           txnbuild.NewInfiniteTimeout(),
		IncrementSequenceNum: false,
	}
	tx, err := txnbuild.NewTransaction(txParams)
	tt.NoError(err)
	preAuthHash, err := tx.Hash(test.IntegrationNetworkPassphrase)
	tt.NoError(err)

	// Some operation re-shuffling:
	// 1. Let's move the account creation before the sandwich
	// 2. Instead, include the preauthorized signature in the sandwich
	ops2 := make([]txnbuild.Operation, 4, 4)
	ops[0] = ops[1]
	copy(ops[1:], ops[:])
	preAuthSignerKey := xdr.SignerKey{
		Type:      xdr.SignerKeyTypeSignerKeyTypePreAuthTx,
		PreAuthTx: (*xdr.Uint256)(&preAuthHash),
	}
	ops[1] = &txnbuild.SetOptions{
		Signer: &txnbuild.Signer{
			Address: preAuthSignerKey.Address(),
			Weight:  0,
		},
	}

	signers := []*keypair.Full{sponsorPair, newAccountPair}
	txResp, err := itest.SubmitMultiSigOperations(sponsor, signers, ops2...)
	tt.NoError(err)

	var txResult xdr.TransactionResult
	err = xdr.SafeUnmarshalBase64(txResp.ResultXdr, &txResult)
	tt.NoError(err)
	tt.Equal(xdr.TransactionResultCodeTxSuccess, txResult.Result.Code)

	// Submit the preauthorized transaction
	txB64, err := tx.Base64()
	tt.NoError(err)
	txResp, err = itest.Client().SubmitTransactionXDR(txB64)
	tt.NoError(err)
	err = xdr.SafeUnmarshalBase64(txResp.ResultXdr, &txResult)
	tt.NoError(err)
	tt.Equal(xdr.TransactionResultCodeTxSuccess, txResult.Result.Code)
}
