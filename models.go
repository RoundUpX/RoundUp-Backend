package main

import (
	"time"
)

// define magic numbers
const BaseRoundupPercent = 0.05
const RecentPeriodDays = 7
const MinPressure = 0.3
const MaxPressure = 4
const DefaultAvgTxnsPerDay = 3

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

type UserPreferences struct {
	RoundupCategories []string    `json:"roundup_categories"` // things like "food", "clothes", "groceries"
	GoalName          string      `json:"goal_name"`          // trip
	GoalAmount        float64     `json:"goal_amount"`        // 5000
	TargetDate        time.Time   `json:"target_date"`        // 4th May
	CurrentSavings    float64     `json:"current_savings"`    // amount already saved
	AverageRoundup    float64     `json:"average_roundup"`    // average amount roundedup per txn in last 30 txns
	RoundupHistory    []float64   `json:"roundup_history"`    // contains all roundups done in past
	RoundupDates      []time.Time `json:"roundup_dates"`      // stores when the roundup took place
}

type Transaction struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Amount    float64   `json:"amount"`
	Category  string    `json:"category"`
	Roundup   float64   `json:"roundup"`
	CreatedAt time.Time `json:"created_at"`
	Merchant  string    `json:"merchant"` // upi
}

type TransactionService struct {
	repo      TransactionRepository
	userRepo  UserRepository
	upiClient UPIClient
}

type TransactionRepository interface {
	SaveTransaction(tx Transaction) error
	GetTransactionsByUserID(userID string) ([]Transaction, error)
	GetTransactionByID(id string) (*Transaction, error)
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
