package main

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// bankServer implements the Bank gRPC service.
type bankServer struct {
	UnimplementedBankServer
	allAccounts *accounts
}

func (s *bankServer) OpenAccount(ctx context.Context, req *OpenAccountRequest) (*Account, error) {
	cust := getCustomer(ctx)
	if cust == "" {
		return nil, status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}
	switch req.Type {
	case Account_CHECKING, Account_SAVING, Account_MONEY_MARKET:
		if req.InitialDepositCents < 0 {
			return nil, status.Errorf(codes.InvalidArgument, "initial deposit amount cannot be negative: %s", dollars(req.InitialDepositCents))
		}
	case Account_LINE_OF_CREDIT, Account_LOAN, Account_EQUITIES:
		if req.InitialDepositCents != 0 {
			return nil, status.Errorf(codes.InvalidArgument, "initial deposit amount must be zero for account type %v: %s", req.Type, dollars(req.InitialDepositCents))
		}
	default:
		return nil, status.Errorf(codes.InvalidArgument, "invalid account type: %v", req.Type)
	}

	return s.allAccounts.openAccount(cust, req.Type, req.InitialDepositCents), nil
}

func (s *bankServer) CloseAccount(ctx context.Context, req *CloseAccountRequest) (*empty.Empty, error) {
	cust := getCustomer(ctx)
	if cust == "" {
		return nil, status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	if err := s.allAccounts.closeAccount(cust, req.AccountNumber); err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil
}

func (s *bankServer) GetAccounts(ctx context.Context, _ *empty.Empty) (*GetAccountsResponse, error) {
	cust := getCustomer(ctx)
	if cust == "" {
		return nil, status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	accounts := s.allAccounts.getAllAccounts(cust)
	return &GetAccountsResponse{Accounts: accounts}, nil
}

func (s *bankServer) GetTransactions(req *GetTransactionsRequest, stream Bank_GetTransactionsServer) error {
	cust := getCustomer(stream.Context())
	if cust == "" {
		return status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	acct, err := s.allAccounts.getAccount(cust, req.AccountNumber)
	if err != nil {
		return err
	}

	var start, end time.Time
	if req.Start != nil {
		err := req.Start.CheckValid()
		if err != nil {
			return err
		}
		start = req.Start.AsTime()
	}
	if req.End != nil {
		err := req.End.CheckValid()
		if err != nil {
			return err
		}
		end = req.End.AsTime()
	} else {
		end = time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.Local)
	}

	txns := acct.getTransactions()
	for _, txn := range txns {
		err := txn.Date.CheckValid()
		if err != nil {
			return err
		}
		t := txn.Date.AsTime()
		if (t.After(start) || t.Equal(start)) &&
			(t.Before(end) || t.Equal(end)) {

			if err := stream.Send(txn); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *bankServer) Deposit(ctx context.Context, req *DepositRequest) (*BalanceResponse, error) {
	cust := getCustomer(ctx)
	if cust == "" {
		return nil, status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	switch req.Source {
	case DepositRequest_ACH, DepositRequest_CASH, DepositRequest_CHECK, DepositRequest_WIRE:
		// ok
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unknown deposit source: %v", req.Source)
	}

	if req.AmountCents <= 0 {
		return nil, status.Errorf(codes.InvalidArgument, "deposit amount cannot be non-positive: %s", dollars(req.AmountCents))
	}

	desc := fmt.Sprintf("%v deposit", req.Source)
	if req.Desc != "" {
		desc = fmt.Sprintf("%s: %s", desc, req.Desc)
	}
	acct, err := s.allAccounts.getAccount(cust, req.AccountNumber)
	if err != nil {
		return nil, err
	}
	newBalance, err := acct.newTransaction(req.AmountCents, desc)
	if err != nil {
		return nil, err
	}
	return &BalanceResponse{
		AccountNumber: req.AccountNumber,
		BalanceCents:  newBalance,
	}, nil
}

func (s *bankServer) Withdraw(ctx context.Context, req *WithdrawRequest) (*BalanceResponse, error) {
	cust := getCustomer(ctx)
	if cust == "" {
		return nil, status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	if req.AmountCents >= 0 {
		return nil, status.Errorf(codes.InvalidArgument, "withdrawal amount cannot be non-negative: %s", dollars(req.AmountCents))
	}

	acct, err := s.allAccounts.getAccount(cust, req.AccountNumber)
	if err != nil {
		return nil, err
	}
	newBalance, err := acct.newTransaction(req.AmountCents, req.Desc)
	if err != nil {
		return nil, err
	}
	return &BalanceResponse{
		AccountNumber: req.AccountNumber,
		BalanceCents:  newBalance,
	}, nil
}

func (s *bankServer) Transfer(ctx context.Context, req *TransferRequest) (*TransferResponse, error) {
	cust := getCustomer(ctx)
	if cust == "" {
		return nil, status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	if req.AmountCents <= 0 {
		return nil, status.Errorf(codes.InvalidArgument, "transfer amount cannot be non-positive: %s", dollars(req.AmountCents))
	}

	var srcAcct *account
	var srcDesc string
	switch src := req.Source.(type) {
	case *TransferRequest_ExternalSource:
		srcDesc = fmt.Sprintf("ACH %09d:%06d", src.ExternalSource.AchRoutingNumber, src.ExternalSource.AchAccountNumber)
		if src.ExternalSource.AchAccountNumber == 0 || src.ExternalSource.AchRoutingNumber == 0 {
			return nil, status.Errorf(codes.InvalidArgument, "external source routing and account numbers cannot be zero: %s", srcDesc)
		}
	case *TransferRequest_SourceAccountNumber:
		srcDesc = fmt.Sprintf("account %06d", src.SourceAccountNumber)
		var err error
		if srcAcct, err = s.allAccounts.getAccount(cust, src.SourceAccountNumber); err != nil {
			return nil, err
		}
	}

	var destAcct *account
	var destDesc string
	switch dest := req.Dest.(type) {
	case *TransferRequest_ExternalDest:
		destDesc = fmt.Sprintf("ACH %09d:%06d", dest.ExternalDest.AchRoutingNumber, dest.ExternalDest.AchAccountNumber)
		if dest.ExternalDest.AchAccountNumber == 0 || dest.ExternalDest.AchRoutingNumber == 0 {
			return nil, status.Errorf(codes.InvalidArgument, "external source routing and account numbers cannot be zero: %s", destDesc)
		}
	case *TransferRequest_DestAccountNumber:
		destDesc = fmt.Sprintf("account %06d", dest.DestAccountNumber)
		var err error
		if destAcct, err = s.allAccounts.getAccount(cust, dest.DestAccountNumber); err != nil {
			return nil, err
		}
	}

	var srcBalance int32
	if srcAcct != nil {
		desc := fmt.Sprintf("transfer to %s", destDesc)
		if req.Desc != "" {
			desc = fmt.Sprintf("%s: %s", desc, req.Desc)
		}
		var err error
		if srcBalance, err = srcAcct.newTransaction(-req.AmountCents, desc); err != nil {
			return nil, err
		}
	}

	var destBalance int32
	if destAcct != nil {
		desc := fmt.Sprintf("transfer from %s", srcDesc)
		if req.Desc != "" {
			desc = fmt.Sprintf("%s: %s", desc, req.Desc)
		}
		var err error
		if destBalance, err = destAcct.newTransaction(req.AmountCents, desc); err != nil {
			return nil, err
		}
	}

	return &TransferResponse{
		SrcAccountNumber:  req.GetSourceAccountNumber(),
		SrcBalanceCents:   srcBalance,
		DestAccountNumber: req.GetDestAccountNumber(),
		DestBalanceCents:  destBalance,
	}, nil
}
