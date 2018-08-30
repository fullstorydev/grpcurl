package main

import (
	"bytes"
	"sync"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/ptypes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// In-memory database that is periodically saved to a JSON file.

type accounts struct {
	AccountNumbersByCustomer map[string][]uint64
	AccountsByNumber         map[uint64]*account
	AccountNumbers           []uint64
	Customers                []string
	LastAccountNum           uint64
	mu                       sync.RWMutex
}

type account struct {
	Account
	Transactions []*Transaction
	mu           sync.RWMutex
}

func (a *account) newTransaction(amountCents int32, desc string) (int32, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	newBalance := a.BalanceCents + amountCents
	if newBalance < 0 {
		return 0, status.Errorf(codes.FailedPrecondition, "insufficient funds: cannot withdraw %s when balance is %s", dollars(amountCents), dollars(a.BalanceCents))
	}
	a.BalanceCents += amountCents
	a.Transactions = append(a.Transactions, &Transaction{
		AccountNumber: a.AccountNumber,
		Date:          ptypes.TimestampNow(),
		AmountCents:   amountCents,
		SeqNumber:     uint64(len(a.Transactions) + 1),
		Desc:          desc,
	})
	return a.BalanceCents, nil
}

func (t *Transaction) MarshalJSON() ([]byte, error) {
	var jsm jsonpb.Marshaler
	var buf bytes.Buffer
	if err := jsm.Marshal(&buf, t); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (t *Transaction) UnmarshalJSON(b []byte) error {
	return jsonpb.Unmarshal(bytes.NewReader(b), t)
}

func (a *accounts) Clone() *accounts {
	var clone accounts
	clone.AccountNumbersByCustomer = map[string][]uint64{}
	clone.AccountsByNumber = map[uint64]*account{}

	a.mu.RLock()
	clone.Customers = a.Customers
	a.mu.RUnlock()

	for _, cust := range clone.Customers {
		var acctNums []uint64
		a.mu.RLock()
		acctNums = a.AccountNumbersByCustomer[cust]
		a.mu.RUnlock()

		clone.AccountNumbersByCustomer[cust] = acctNums
		clone.AccountNumbers = append(clone.AccountNumbers, acctNums...)

		for _, acctNum := range acctNums {
			a.mu.RLock()
			acct := a.AccountsByNumber[acctNum]
			a.mu.RUnlock()

			acct.mu.RLock()
			txns := acct.Transactions
			acct.mu.RUnlock()

			clone.AccountsByNumber[acctNum] = &account{Transactions: txns}
		}
	}

	return &clone
}
