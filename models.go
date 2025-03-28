package main

import (
	"time"
)

// define magic numbers
const BaseRoundupPercent = 0.05
const RecentPeriodDays = 7
const MinPressure = 0.3
const MaxPressure = 3
const DefaultAvgTxnsPerDay = 3
const DefaultAvgTxnRoundup = 10

const roundUpAccount = "meet1771.mm@okhdfcbank"

// Define all structs
type User struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Email       string          `json:"email"`
	Password    string          `json:"-"` // omit from JSON responses
	Preferences UserPreferences `json:"preferences"`
	CreatedAt   time.Time       `json:"created_at"`
}

type VerifyUPIURIRequest struct {
	UPIURI string `json:"upi_uri" binding:"required"`
}

type UserPreferences struct {
	RoundupCategories []string    `json:"roundup_categories"` // things like "food", "clothes", "groceries"
	GoalName          string      `json:"goal_name"`          // trip
	GoalAmount        float64     `json:"goal_amount"`        // 5000
	TargetDate        time.Time   `json:"target_date"`        // 4th May
	CurrentSavings    float64     `json:"current_savings"`    // amount already saved
	RoundupHistory    []float64   `json:"roundup_history"`    // contains all roundups done in past
	RoundupDates      []time.Time `json:"roundup_dates"`      // stores when the roundup took place
}

type Wallet struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Balance     float64   `json:"balance"`
	LastUpdated time.Time `json:"last_updated"`
}

type WalletTransaction struct {
	ID          string    `json:"id"`
	WalletID    string    `json:"wallet_id"`
	Amount      float64   `json:"amount"`
	Type        string    `json:"type"` // "credit" or "debit"
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Transaction struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Amount         float64   `json:"amount"`
	Category       string    `json:"category"`
	Roundup        float64   `json:"roundup"`
	CreatedAt      time.Time `json:"created_at"`
	Merchant       string    `json:"merchant"` // upi id
	RoundupEnabled bool      `json:"roundup_enabled"`
}

type TransactionService struct {
	repo       TransactionRepository
	userRepo   UserRepository
	upiClient  UPIClient
	walletRepo WalletRepository
}

type TransactionRepository interface {
	SaveTransaction(tx Transaction) error
	GetTransactionsByUserID(userID string) ([]Transaction, error)
	GetTransactionByID(id string) (*Transaction, error)
	GetTotalRoundupInPeriod(days int) (float64, error)
}

type UserRepository interface {
	FindByID(id string) (*User, error)
	Update(user *User) error
	CreateUserPreferences(userID string, prefs UserPreferences) error
	UpdatePreferences(userID string, prefs UserPreferences) error
	CreateUser(user *User) error
	GetUserByEmail(email string) (*User, error)
}

type UPIClient interface {
	GenerateUPIURI(txn Transaction, toAccount string, amount float64) (string, error)
}

type WalletRepository interface {
	CreateWallet(wallet Wallet) error
	GetWalletByUserID(userID string) (*Wallet, error)
	UpdateWalletBalance(walletID string, newBalance float64) error
	AddWalletTransaction(tx WalletTransaction) error
	GetWalletTransactions(walletID string) ([]WalletTransaction, error)
}
