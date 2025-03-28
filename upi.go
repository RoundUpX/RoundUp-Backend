package main

import (
	"fmt"
	"net/url"
)

// DummyUPIClient implementation
type DummyUPIClient struct{}

func (d *DummyUPIClient) GenerateUPIURI(txn Transaction, toAccount string, amount float64) (string, error) {
	upiURI := url.URL{
		Scheme: "upi",
		Host:   "pay",
	}

	query := url.Values{}
	query.Add("pa", toAccount)                   // Payee address
	query.Add("pn", "RoundUp")                   // Payee name
	query.Add("tr", txn.ID)                      // Transaction reference ID
	query.Add("tn", txn.Category)                // Transaction note
	query.Add("am", fmt.Sprintf("%.2f", amount)) // amount
	query.Add("cu", "INR")                       // currency
	query.Add("url", "www.github.com/RoundUpX")  // URL. additional details

	upiURI.RawQuery = query.Encode()

	return upiURI.String(), nil
}

func (s *TransactionService) generateUPIURIs(transaction Transaction) (string, string, error) {

	merchantURI, err := s.upiClient.GenerateUPIURI(transaction, transaction.Merchant, transaction.Amount)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate UPI URI for merchant: %v", err)
	}

	roundUpURI, err := s.upiClient.GenerateUPIURI(transaction, roundUpAccount, transaction.Roundup)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate UPI URI for RoundUp: %v", err)
	}

	return merchantURI, roundUpURI, nil
}
