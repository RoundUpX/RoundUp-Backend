package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Business logic functions
func (s *TransactionService) ProcessRoundup(userID string, transaction Transaction) (string, string, error) {
	// find the user in userRepo to get their preferences
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return "", "", fmt.Errorf("User not found: %v", err)
	}

	if user.Preferences.GoalAmount == 0 || user.Preferences.TargetDate.IsZero() || user.Preferences.TargetDate.Before(time.Now()) {
		// FIXME
		fmt.Println("No goal amount. considering base roundup")

		// If user has no goal, just save the base amount
		Roundup := slabBasedRoundup(transaction.Amount*(1+BaseRoundupPercent)) - transaction.Amount
		Roundup = math.Max(0, Roundup)
		transaction.Roundup = Roundup

		user.Preferences.RoundupHistory = append(user.Preferences.RoundupHistory, Roundup)
		user.Preferences.RoundupDates = append(user.Preferences.RoundupDates, time.Now())

		// add to the transaction repo
		if err := s.repo.SaveTransaction(transaction); err != nil {
			return "", "", fmt.Errorf("failed to update transaction: %v", err)
		}

		// update user pref
		user.Preferences.CurrentSavings += Roundup
		user.Preferences.RoundupHistory = append(user.Preferences.RoundupHistory, Roundup)
		user.Preferences.RoundupDates = append(user.Preferences.RoundupDates, time.Now())

		// Save pref to user repo
		if err := s.userRepo.UpdatePreferences(userID, user.Preferences); err != nil {
			return "", "", fmt.Errorf("failed to update user preferences: %v", err)
		}

		// UPI Part
		merchantURI, err := s.upiClient.GenerateUPIURI(transaction, transaction.Merchant, transaction.Amount)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate UPI URI for merchant: %v", err)
		}

		roundupURI, err := s.upiClient.GenerateUPIURI(transaction, roundUpAccount, transaction.Amount)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate UPI URI for RoundUp: %v", err)
		}

		return merchantURI, roundupURI, nil
	}

	// if the transaction category is not present in the users pref category, return nill
	if len(user.Preferences.RoundupCategories) > 0 && !contains(user.Preferences.RoundupCategories, transaction.Category) {
		return "", "", nil
	}

	rawBaseRoundup := slabBasedRoundup(transaction.Amount*(1+BaseRoundupPercent)) - transaction.Amount
	baseRoundup := math.Max(rawBaseRoundup, 0)

	daysRemaining := math.Floor(time.Until(user.Preferences.TargetDate).Hours() / 24)
	// minimum 1 day
	daysRemaining = math.Max(1, daysRemaining)

	remainingAmount := user.Preferences.GoalAmount - user.Preferences.CurrentSavings
	requiredTxns := math.Floor(remainingAmount / user.Preferences.AverageRoundup)

	// define the period for recent transactions
	recentPeriod := RecentPeriodDays * 24 * time.Hour
	cutoff := time.Now().Add(-recentPeriod)

	// filter transactions to only include those within the last week
	recentDates := []time.Time{}
	for _, txnDate := range user.Preferences.RoundupDates {
		if txnDate.After(cutoff) {
			recentDates = append(recentDates, txnDate)
		}
	}

	// Calculate the average transactions per day over the recent period
	var avgTxnsPerDay float64
	if len(recentDates) > 0 {
		avgTxnsPerDay = float64(len(recentDates)) / RecentPeriodDays
	} else {
		avgTxnsPerDay = DefaultAvgTxnsPerDay
	}

	// Calculate how many txns we think will happen until GoalDate
	projectedTxns := avgTxnsPerDay * daysRemaining

	// pressure must be adjusted on the basis of time left, proj txns and stuff
	pressure := 1.0
	if requiredTxns > 0 && projectedTxns > 0 {
		pressure = requiredTxns / projectedTxns
	}

	// clip pressure
	pressure = math.Min(math.Max(pressure, MinPressure), MaxPressure)

	Roundup := baseRoundup * pressure

	transaction.Roundup = Roundup // roundup to 2 digits

	// add to the transaction repo
	if err := s.repo.SaveTransaction(transaction); err != nil {
		return "", "", fmt.Errorf("failed to update transaction: %v", err)
	}

	// update user pref
	user.Preferences.CurrentSavings += Roundup
	user.Preferences.RoundupHistory = append(user.Preferences.RoundupHistory, Roundup)
	user.Preferences.RoundupDates = append(user.Preferences.RoundupDates, time.Now())

	// Save pref to user repo
	if err := s.userRepo.UpdatePreferences(userID, user.Preferences); err != nil {
		return "", "", fmt.Errorf("failed to update user preferences: %v", err)
	}

	// UPI Part
	merchantURI, err := s.upiClient.GenerateUPIURI(transaction, transaction.Merchant, transaction.Amount)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate UPI URI for merchant: %v", err)
	}

	roundupURI, err := s.upiClient.GenerateUPIURI(transaction, roundUpAccount, transaction.Amount)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate UPI URI for RoundUp: %v", err)
	}

	return merchantURI, roundupURI, nil
}

func slabBasedRoundup(amount float64) float64 {
	// TODO
	// Maximum extra amount user wouldn't mind paying (8% of the transaction amount)
	maxRoundup := 0.08 * amount

	// Define possible increments (in descending order to try larger roundups first)
	units := []float64{100, 50, 10, 5, 1}

	var bestRoundup float64 = 0

	for _, unit := range units {
		// Calculate the roundup if we round up to the next multiple of unit
		candidate := math.Ceil(amount/unit)*unit - amount
		// Choose the candidate if it is within the acceptable threshold and is larger than what we had before
		if candidate <= maxRoundup && candidate > bestRoundup {
			bestRoundup = candidate
		}
	}

	// Fallback: if none of the units produce a roundup within the threshold, round up to the nearest whole number.
	if bestRoundup == 0 {
		bestRoundup = math.Ceil(amount) - amount
	}

	return bestRoundup
}

func contains(categories []string, target string) bool {
	for _, cat := range categories {
		if strings.EqualFold(cat, target) {
			return true
		}
	}
	return false
}
