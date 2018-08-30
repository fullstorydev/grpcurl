package main

import (
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// bankServer implements the Bank gRPC service.
type bankServer struct {
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

	s.allAccounts.mu.Lock()
	defer s.allAccounts.mu.Unlock()
	accountNums, ok := s.allAccounts.AccountNumbersByCustomer[cust]
	if !ok {
		// no accounts for this customer? it's a new customer
		s.allAccounts.Customers = append(s.allAccounts.Customers, cust)
	}
	num := s.allAccounts.LastAccountNum + 1
	s.allAccounts.LastAccountNum = num
	s.allAccounts.AccountNumbers = append(s.allAccounts.AccountNumbers, num)
	accountNums = append(accountNums, num)
	s.allAccounts.AccountNumbersByCustomer[cust] = accountNums
	var acct account
	acct.AccountNumber = num
	acct.BalanceCents = req.InitialDepositCents
	acct.Transactions = append(acct.Transactions, &Transaction{
		AccountNumber: num,
		SeqNumber:     1,
		Date:          ptypes.TimestampNow(),
		AmountCents:   req.InitialDepositCents,
		Desc:          "initial deposit",
	})
	s.allAccounts.AccountsByNumber[num] = &acct
	return &acct.Account, nil
}

func (s *bankServer) CloseAccount(ctx context.Context, req *CloseAccountRequest) (*empty.Empty, error) {
	cust := getCustomer(ctx)
	if cust == "" {
		return nil, status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	s.allAccounts.mu.Lock()
	defer s.allAccounts.mu.Unlock()
	acctNums := s.allAccounts.AccountNumbersByCustomer[cust]
	found := -1
	for i, num := range acctNums {
		if num == req.AccountNumber {
			found = i
			break
		}
	}
	if found == -1 {
		return nil, status.Errorf(codes.NotFound, "you have no account numbered %d", req.AccountNumber)
	}

	for i, num := range s.allAccounts.AccountNumbers {
		if num == req.AccountNumber {
			s.allAccounts.AccountNumbers = append(s.allAccounts.AccountNumbers[:i], s.allAccounts.AccountNumbers[i+1:]...)
			break
		}
	}

	acct := s.allAccounts.AccountsByNumber[req.AccountNumber]
	if acct.BalanceCents != 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "account %d cannot be closed because it has a non-zero balance: %s", req.AccountNumber, dollars(acct.BalanceCents))
	}
	s.allAccounts.AccountNumbersByCustomer[cust] = append(acctNums[:found], acctNums[found+1:]...)
	delete(s.allAccounts.AccountsByNumber, req.AccountNumber)
	return &empty.Empty{}, nil
}

func (s *bankServer) GetAccounts(ctx context.Context, _ *empty.Empty) (*GetAccountsResponse, error) {
	cust := getCustomer(ctx)
	if cust == "" {
		return nil, status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	s.allAccounts.mu.RLock()
	defer s.allAccounts.mu.RUnlock()
	accountNums := s.allAccounts.AccountNumbersByCustomer[cust]
	var accounts []*Account
	for _, num := range accountNums {
		accounts = append(accounts, &s.allAccounts.AccountsByNumber[num].Account)
	}
	return &GetAccountsResponse{Accounts: accounts}, nil
}

func (s *bankServer) GetTransactions(req *GetTransactionsRequest, stream Bank_GetTransactionsServer) error {
	cust := getCustomer(stream.Context())
	if cust == "" {
		return status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	acct, err := func() (*account, error) {
		s.allAccounts.mu.Lock()
		defer s.allAccounts.mu.Unlock()
		acctNums := s.allAccounts.AccountNumbersByCustomer[cust]
		for _, num := range acctNums {
			if num == req.AccountNumber {
				return s.allAccounts.AccountsByNumber[num], nil
			}
		}
		return nil, status.Errorf(codes.NotFound, "you have no account numbered %d", req.AccountNumber)
	}()
	if err != nil {
		return err
	}

	var start, end time.Time
	if req.Start != nil {
		start, err = ptypes.Timestamp(req.Start)
		if err != nil {
			return err
		}
	}
	if req.End != nil {
		end, err = ptypes.Timestamp(req.End)
		if err != nil {
			return err
		}
	} else {
		end = time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.Local)
	}

	acct.mu.RLock()
	txns := acct.Transactions
	acct.mu.RUnlock()

	for _, txn := range txns {
		t, err := ptypes.Timestamp(txn.Date)
		if err != nil {
			return err
		}
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
	newBalance, err := s.newTransaction(cust, req.AccountNumber, req.AmountCents, desc)
	if err != nil {
		return nil, err
	}
	return &BalanceResponse{
		AccountNumber: req.AccountNumber,
		BalanceCents:  newBalance,
	}, nil
}

func (s *bankServer) getAccount(cust string, acctNumber uint64) (*account, error) {
	s.allAccounts.mu.Lock()
	defer s.allAccounts.mu.Unlock()
	acctNums := s.allAccounts.AccountNumbersByCustomer[cust]
	for _, num := range acctNums {
		if num == acctNumber {
			return s.allAccounts.AccountsByNumber[num], nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "you have no account numbered %d", acctNumber)
}

func (s *bankServer) newTransaction(cust string, acctNumber uint64, amountCents int32, desc string) (int32, error) {
	acct, err := s.getAccount(cust, acctNumber)
	if err != nil {
		return 0, err
	}
	return acct.newTransaction(amountCents, desc)
}

func (s *bankServer) Withdraw(ctx context.Context, req *WithdrawRequest) (*BalanceResponse, error) {
	cust := getCustomer(ctx)
	if cust == "" {
		return nil, status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	if req.AmountCents >= 0 {
		return nil, status.Errorf(codes.InvalidArgument, "withdrawal amount cannot be non-negative: %s", dollars(req.AmountCents))
	}

	newBalance, err := s.newTransaction(cust, req.AccountNumber, req.AmountCents, req.Desc)
	if err != nil {
		return nil, err
	}
	return &BalanceResponse{
		AccountNumber: req.AccountNumber,
		BalanceCents:  newBalance,
	}, nil
}

func dollars(amountCents int32) string {
	return fmt.Sprintf("$%02f", float64(amountCents)/100)
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
		if srcAcct, err = s.getAccount(cust, src.SourceAccountNumber); err != nil {
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
		if destAcct, err = s.getAccount(cust, dest.DestAccountNumber); err != nil {
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
