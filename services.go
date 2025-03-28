package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"google.golang.org/api/option"
)

func extractUPIDetails(upiURI string) (string, string, string, error) {
	parsedURI, err := url.Parse(upiURI)
	if err != nil || !strings.HasPrefix(upiURI, "upi://pay") {
		return "", "", "", err
	}

	queryParams := parsedURI.Query()
	upiID := queryParams.Get("pa") // Payee Address
	merchantName := queryParams.Get("pn")
	currency := queryParams.Get("cu")

	// Ensure UPI ID exists
	if upiID == "" {
		return "", "", "", err
	}

	return upiID, merchantName, currency, nil
}

func findTransactionType(upiID, payeeName string) (int, error) {

	ctx := context.Background()

	client, err := genai.NewClient(ctx, option.WithAPIKey("AIzaSyCBhES1vOqE5OQyHXjffVhNIvhry2mxG1Q"))
	if err != nil {
		return -1, fmt.Errorf("failed to create client: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel("gemini-1.5-flash")
	resp, err := model.GenerateContent(ctx, genai.Text(fmt.Sprintf("Transaction Categorization: Given the following transaction details: UPI ID: %s Payee Name: %s Please determine the most appropriate category for this transaction from the following list: Groceries, Rent & Utilities, Transportation, Healthcare, Dining & Food, Clothing & Accessories, Entertainment, Personal Care, Investments, Debt & Loans, Savings & Emergency Fund, Education, Gifts & Donations, Technology & Gadgets, Travel, Subscriptions & Memberships. Provide just the category name as your response.", upiID, payeeName)))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Generated content:", resp.Candidates[0].Content.Parts[0])

	for i, category := range categories {
		if payeeName == category {
			return i, nil
		}
	}

	return -1, nil // Return -1 for miscellaneous if no match found
}

// Business logic functions
func (s *TransactionService) ProcessRoundup(userID string, transaction Transaction) (float64, string, string, error) {

	if !transaction.RoundupEnabled {

		transaction.Roundup = 0.0
		uri1, _, err := s.generateUPIURIs(transaction)
		if err != nil {
			log.Printf("Error generating UPI URIs: %v\n", err)
			return 0.0, "", "", err
		}

		return 0.0, uri1, "", nil
	}

	// Find the user in userRepo to get their preferences
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		log.Printf("Error finding user: %v\n", err)
		return 0.0, "", "", fmt.Errorf("User not found: %v", err)
	}

	// Validate goal details
	if user.Preferences.GoalAmount == 0 || user.Preferences.TargetDate.IsZero() || user.Preferences.TargetDate.Before(time.Now()) {
		log.Println("No valid goal. Falling back to base roundup.")
		RoundUp, uri1, uri2, err := s.processBaseRoundup(userID, transaction)
		if err != nil {
			return 0.0, "", "", err
		}
		return RoundUp, uri1, uri2, nil
	}

	// Check if transaction category matches user preferences
	if len(user.Preferences.RoundupCategories) > 0 && !contains(user.Preferences.RoundupCategories, transaction.Category) {
		log.Printf("Transaction category '%s' does not match user preferences. Skipping.\n", transaction.Category)
		return 0.0, "", "", nil
	}

	// Calculate raw base roundup
	rawBaseRoundup := (transaction.Amount * BaseRoundupPercent)
	baseRoundup := math.Max(rawBaseRoundup, 0)

	// Calculate days remaining until the target date
	daysRemaining := math.Floor(time.Until(user.Preferences.TargetDate).Hours() / 24)
	daysRemaining = math.Max(1, daysRemaining) // Ensure minimum of 1 day

	averageRoundup := s.calculateAvgRoundup()

	remainingAmount := user.Preferences.GoalAmount - user.Preferences.CurrentSavings

	if remainingAmount < 0 {
		RoundUp, uri1, uri2, err := s.processBaseRoundup(userID, transaction)
		if err != nil {
			return 0.0, "", "", err
		}
		return RoundUp, uri1, uri2, nil
	}

	requiredTxns := math.Floor(remainingAmount / averageRoundup)

	recentDates := filterRecentDates(user.Preferences.RoundupDates, RecentPeriodDays)

	avgTxnsPerDay := calculateAvgTxnsPerDay(recentDates, RecentPeriodDays)

	projectedTxns := avgTxnsPerDay * daysRemaining

	pressure := calculatePressure(requiredTxns, projectedTxns)

	Roundup := math.Min(baseRoundup*pressure, remainingAmount)

	log.Printf("Base Roundup: %.2f", baseRoundup)
	log.Printf("Days Remaining: %.2f", daysRemaining)
	log.Printf("Average Roundup: %.2f", averageRoundup)
	log.Printf("Remaining Amount: %.2f", remainingAmount)
	log.Printf("Required Transactions: %.2f", requiredTxns)
	log.Printf("Projected Transactions: %.2f", projectedTxns)
	log.Printf("Pressure: %.2f", pressure)

	if Roundup < 1 {
		log.Printf("Calculated roundup %.2f is below threshold. Skipping.\n", Roundup)
		return 0.0, "", "", nil
	}

	transaction.Roundup = Roundup

	err = s.saveTransactionAndPreferences(userID, transaction, Roundup)
	if err != nil {
		log.Printf("Error saving transaction and preferences: %v\n", err)
		return 0.0, "", "", err
	}

	if Roundup > 0 {
		// Add roundup amount to user's wallet
		err = s.AddToWallet(userID, Roundup, fmt.Sprintf("Roundup from %s transaction of ₹%.2f", transaction.Category, transaction.Amount))
		if err != nil {
			log.Printf("Error adding roundup to wallet: %v\n", err)
		}
	}

	uri1, uri2, err := s.generateUPIURIs(transaction)
	if err != nil {
		log.Printf("Error generating UPI URIs: %v\n", err)
		return 0.0, "", "", err
	}

	return Roundup, uri1, uri2, nil
}

func (s *TransactionService) processBaseRoundup(userID string, transaction Transaction) (float64, string, string, error) {

	Roundup := transaction.Amount * BaseRoundupPercent
	Roundup = math.Max(0, Roundup)

	transaction.Roundup = Roundup

	err := s.saveTransactionAndPreferences(userID, transaction, Roundup)
	if err != nil {
		return 0.0, "", "", err
	}

	uri1, uri2, err := s.generateUPIURIs(transaction)
	if err != nil {
		return 0.0, "", "", err
	}

	return Roundup, uri1, uri2, nil
}

func filterRecentDates(dates []time.Time, recentDays int) []time.Time {
	cutoff := time.Now().Add(-time.Duration(recentDays) * 24 * time.Hour)
	var recentDates []time.Time
	for _, date := range dates {
		if date.After(cutoff) {
			recentDates = append(recentDates, date)
		}
	}
	return recentDates
}

func calculateAvgTxnsPerDay(recentDates []time.Time, recentDays int) float64 {
	if len(recentDates) == 0 {
		return DefaultAvgTxnsPerDay
	}
	return float64(len(recentDates)) / float64(recentDays)
}

func (s *TransactionService) calculateAvgRoundup() float64 {
	// get total rounded-up amount from transactions in the last 7 days
	totalRoundup, err := s.repo.GetTotalRoundupInPeriod(7)
	if err != nil {
		log.Printf("Error fetching total roundup from DB: %v", err)
		return 10 // return default value
	}

	// Calculate average roundup over the last 7 days
	return totalRoundup / 7
}

func calculatePressure(requiredTxns, projectedTxns float64) float64 {
	pressure := MinPressure
	if projectedTxns > 0 {
		pressure = math.Max(requiredTxns/projectedTxns, MinPressure)
	}
	return math.Min(pressure, MaxPressure)
}

func contains(categories []string, target string) bool {
	for _, cat := range categories {
		if strings.EqualFold(cat, target) {
			return true
		}
	}
	return false
}

func (s *TransactionService) saveTransactionAndPreferences(userID string, transaction Transaction, roundup float64) error {
	transaction.CreatedAt = time.Now()
	err := s.repo.SaveTransaction(transaction)
	if err != nil {
		return fmt.Errorf("failed to save transaction: %v", err)
	}

	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return fmt.Errorf("failed to retrieve user: %v", err)
	}

	user.Preferences.CurrentSavings += roundup
	user.Preferences.RoundupHistory = append(user.Preferences.RoundupHistory, roundup)
	user.Preferences.RoundupDates = append(user.Preferences.RoundupDates, time.Now())

	err = s.userRepo.UpdatePreferences(userID, user.Preferences)
	if err != nil {
		return fmt.Errorf("failed to update user preferences: %v", err)
	}

	return nil
}

func (s *TransactionService) CreateUserWallet(userID string) error {
	wallet := Wallet{
		ID:          uuid.New().String(),
		UserID:      userID,
		Balance:     0.0,
		LastUpdated: time.Now(),
	}
	return s.walletRepo.CreateWallet(wallet)
}

func (s *TransactionService) AddToWallet(userID string, amount float64, description string) error {
	wallet, err := s.walletRepo.GetWalletByUserID(userID)
	if err != nil {
		return fmt.Errorf("failed to get wallet: %v", err)
	}

	// Update wallet balance
	newBalance := wallet.Balance + amount
	err = s.walletRepo.UpdateWalletBalance(wallet.ID, newBalance)
	if err != nil {
		return fmt.Errorf("failed to update wallet balance: %v", err)
	}

	// Record transaction
	tx := WalletTransaction{
		ID:          uuid.New().String(),
		WalletID:    wallet.ID,
		Amount:      amount,
		Type:        "credit",
		Description: description,
		CreatedAt:   time.Now(),
	}
	return s.walletRepo.AddWalletTransaction(tx)
}

func (s *TransactionService) WithdrawFromWallet(userID string, amount float64, description string) error {
	wallet, err := s.walletRepo.GetWalletByUserID(userID)
	if err != nil {
		return fmt.Errorf("failed to get wallet: %v", err)
	}

	// Check if sufficient balance
	if wallet.Balance < amount {
		return fmt.Errorf("insufficient balance")
	}

	// Update wallet balance
	newBalance := wallet.Balance - amount
	err = s.walletRepo.UpdateWalletBalance(wallet.ID, newBalance)
	if err != nil {
		return fmt.Errorf("failed to update wallet balance: %v", err)
	}

	// Record transaction
	tx := WalletTransaction{
		ID:          uuid.New().String(),
		WalletID:    wallet.ID,
		Amount:      amount,
		Type:        "debit",
		Description: description,
		CreatedAt:   time.Now(),
	}
	return s.walletRepo.AddWalletTransaction(tx)
}

func (s *TransactionService) GetWalletBalance(userID string) (float64, error) {
	wallet, err := s.walletRepo.GetWalletByUserID(userID)
	if err != nil {
		return 0, fmt.Errorf("failed to get wallet: %v", err)
	}
	return wallet.Balance, nil
}

func (s *TransactionService) GetWalletTransactions(userID string) ([]WalletTransaction, error) {
	wallet, err := s.walletRepo.GetWalletByUserID(userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet: %v", err)
	}
	return s.walletRepo.GetWalletTransactions(wallet.ID)
}
