package main

import (
	"bytes"
	"fmt"
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

func (a *accounts) openAccount(customer string, accountType Account_Type, initialBalanceCents int32) *Account {
	a.mu.Lock()
	defer a.mu.Unlock()

	accountNums, ok := a.AccountNumbersByCustomer[customer]
	if !ok {
		// no accounts for this customer? it's a new customer
		a.Customers = append(a.Customers, customer)
	}
	num := a.LastAccountNum + 1
	a.LastAccountNum = num
	a.AccountNumbers = append(a.AccountNumbers, num)
	accountNums = append(accountNums, num)
	a.AccountNumbersByCustomer[customer] = accountNums
	var acct account
	acct.AccountNumber = num
	acct.BalanceCents = initialBalanceCents
	acct.Transactions = append(acct.Transactions, &Transaction{
		AccountNumber: num,
		SeqNumber:     1,
		Date:          ptypes.TimestampNow(),
		AmountCents:   initialBalanceCents,
		Desc:          "initial deposit",
	})
	a.AccountsByNumber[num] = &acct
	return &acct.Account
}

func (a *accounts) closeAccount(customer string, accountNumber uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	acctNums := a.AccountNumbersByCustomer[customer]
	found := -1
	for i, num := range acctNums {
		if num == accountNumber {
			found = i
			break
		}
	}
	if found == -1 {
		return status.Errorf(codes.NotFound, "you have no account numbered %d", accountNumber)
	}

	acct := a.AccountsByNumber[accountNumber]
	if acct.BalanceCents != 0 {
		return status.Errorf(codes.FailedPrecondition, "account %d cannot be closed because it has a non-zero balance: %s", accountNumber, dollars(acct.BalanceCents))
	}

	for i, num := range a.AccountNumbers {
		if num == accountNumber {
			a.AccountNumbers = append(a.AccountNumbers[:i], a.AccountNumbers[i+1:]...)
			break
		}
	}

	a.AccountNumbersByCustomer[customer] = append(acctNums[:found], acctNums[found+1:]...)
	delete(a.AccountsByNumber, accountNumber)
	return nil
}

func (a *accounts) getAccount(customer string, accountNumber uint64) (*account, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	acctNums := a.AccountNumbersByCustomer[customer]
	for _, num := range acctNums {
		if num == accountNumber {
			return a.AccountsByNumber[num], nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "you have no account numbered %d", accountNumber)
}

func (a *accounts) getAllAccounts(customer string) []*Account {
	a.mu.RLock()
	defer a.mu.RUnlock()

	accountNums := a.AccountNumbersByCustomer[customer]
	var accounts []*Account
	for _, num := range accountNums {
		accounts = append(accounts, &a.AccountsByNumber[num].Account)
	}
	return accounts
}

type account struct {
	Account
	Transactions []*Transaction
	mu           sync.RWMutex
}

func (a *account) getTransactions() []*Transaction {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Transactions
}

func (a *account) newTransaction(amountCents int32, desc string) (newBalance int32, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	bal := a.BalanceCents + amountCents
	if bal < 0 {
		return 0, status.Errorf(codes.FailedPrecondition, "insufficient funds: cannot withdraw %s when balance is %s", dollars(amountCents), dollars(a.BalanceCents))
	}
	a.BalanceCents = bal
	a.Transactions = append(a.Transactions, &Transaction{
		AccountNumber: a.AccountNumber,
		Date:          ptypes.TimestampNow(),
		AmountCents:   amountCents,
		SeqNumber:     uint64(len(a.Transactions) + 1),
		Desc:          desc,
	})
	return bal, nil
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

func (a *accounts) clone() *accounts {
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

func dollars(amountCents int32) string {
	return fmt.Sprintf("$%02f", float64(amountCents)/100)
}
