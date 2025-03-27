package main

import (
	"fmt"
	"log"
	"math"
	"strings"
	"time"
)

// Business logic functions
func (s *TransactionService) ProcessRoundup(userID string, transaction Transaction) (string, string, error) {
	// Debug: Start of ProcessRoundup
	log.Println("Starting ProcessRoundup for user:", userID)
	log.Printf("Transaction Details: ID=%s, Amount=%.2f, Category=%s\n", transaction.ID, transaction.Amount, transaction.Category)

	// Find the user in userRepo to get their preferences
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		log.Printf("Error finding user: %v\n", err)
		return "", "", fmt.Errorf("User not found: %v", err)
	}

	// Debug: User Preferences
	log.Printf("User Preferences: %+v\n", user.Preferences)

	// Validate goal details
	if user.Preferences.GoalAmount == 0 || user.Preferences.TargetDate.IsZero() || user.Preferences.TargetDate.Before(time.Now()) {
		log.Println("No valid goal. Falling back to base roundup.")
		return s.processBaseRoundup(userID, transaction)
	}

	// Check if transaction category matches user preferences
	if len(user.Preferences.RoundupCategories) > 0 && !contains(user.Preferences.RoundupCategories, transaction.Category) {
		log.Printf("Transaction category '%s' does not match user preferences. Skipping.\n", transaction.Category)
		return "", "", nil
	}

	// Calculate raw base roundup
	rawBaseRoundup := (transaction.Amount * (1 + BaseRoundupPercent)) - transaction.Amount
	baseRoundup := math.Max(rawBaseRoundup, 0)

	// Debug: Base Roundup Calculation
	log.Printf("Raw Base Roundup: %.2f\n", rawBaseRoundup)
	log.Printf("Base Roundup (after max): %.2f\n", baseRoundup)

	// Calculate days remaining until the target date
	daysRemaining := math.Floor(time.Until(user.Preferences.TargetDate).Hours() / 24)
	daysRemaining = math.Max(1, daysRemaining) // Ensure minimum of 1 day

	// Debug: Days Remaining
	log.Printf("Days Remaining until target date: %.0f\n", daysRemaining)

	averageRoundup := math.Max(user.Preferences.AverageRoundup, 10) // Fallback to ₹10 if zero
	remainingAmount := user.Preferences.GoalAmount - user.Preferences.CurrentSavings

	requiredTxns := math.Floor(remainingAmount / averageRoundup)

	// Debug: Goal Calculation
	log.Printf("Remaining Amount to reach goal: %.2f\n", remainingAmount)
	log.Printf("Required Transactions to reach goal: %.0f\n", requiredTxns)

	recentPeriod := RecentPeriodDays * 24 * time.Hour
	cutoff := time.Now().Add(-recentPeriod)

	recentDates := []time.Time{}
	for _, txnDate := range user.Preferences.RoundupDates {
		if txnDate.After(cutoff) {
			recentDates = append(recentDates, txnDate)
		}
	}

	var avgTxnsPerDay float64
	if len(recentDates) > 0 {
		avgTxnsPerDay = float64(len(recentDates)) / RecentPeriodDays
	} else {
		avgTxnsPerDay = DefaultAvgTxnsPerDay // Use default if no recent transactions exist
	}

	projectedTxns := avgTxnsPerDay * daysRemaining

	// Debug: Transaction History and Projections
	log.Printf("Recent Transactions Count: %d\n", len(recentDates))
	log.Printf("Average Transactions Per Day: %.2f\n", avgTxnsPerDay)
	log.Printf("Projected Transactions until target date: %.2f\n", projectedTxns)

	var pressure float64 = MinPressure // Default minimum pressure
	if requiredTxns > 0 && projectedTxns > 0 {
		pressure = math.Max(requiredTxns/projectedTxns, MinPressure)
	}
	pressure = math.Min(pressure, MaxPressure)

	Roundup := baseRoundup * pressure

	// Debug: Pressure and Final Roundup Calculation
	log.Printf("Pressure Factor: %.2f\n", pressure)
	log.Printf("Final Roundup Amount: %.2f\n", Roundup)

	if Roundup < 0.5 { // Lower threshold for skipping roundups
		log.Printf("Calculated roundup %.2f is below threshold. Skipping.\n", Roundup)
		return "", "", nil
	}

	transaction.Roundup = Roundup

	err = s.saveTransactionAndPreferences(userID, transaction, Roundup)
	if err != nil {
		log.Printf("Error saving transaction and preferences: %v\n", err)
		return "", "", err
	}

	return s.generateUPIURIs(transaction)
}

func (s *TransactionService) processBaseRoundup(userID string, transaction Transaction) (string, string, error) {
	Roundup := (transaction.Amount * (1 + BaseRoundupPercent)) - transaction.Amount
	Roundup = math.Max(0, Roundup)

	transaction.Roundup = Roundup

	err := s.saveTransactionAndPreferences(userID, transaction, Roundup)
	if err != nil {
		return "", "", err
	}

	return s.generateUPIURIs(transaction)
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
