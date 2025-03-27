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
	query.Add("pa", toAccount)                       // Payee address
	query.Add("pn", "RoundUp")                       // Payee name
	query.Add("tr", txn.ID)                          // Transaction reference ID
	query.Add("tn", txn.Category)                    // Transaction note
	query.Add("am", fmt.Sprintf("%.2f", txn.Amount)) // amount
	query.Add("cu", "INR")                           // currency
	query.Add("url", "www.github.com/RoundUpX")      // URL. additional details

	upiURI.RawQuery = query.Encode()

	return upiURI.String(), nil
}
