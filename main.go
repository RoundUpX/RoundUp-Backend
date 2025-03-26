package main

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// define magic numbers
// TODO: Decide all these!!!
const BaseRoundupPercent = 0.05
const DefaultAvgTxnThreshold = 30
const RecentPeriodDays = 7
const MinPressure = 0.5
const MaxPressure = 2
const DefaultAvgTxnsPerDay = 2

// Define all structs
type User struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Email       string          `json:"email"`
	Preferences UserPreferences `json:"preferences"`
	CreatedAt   time.Time       `json:"created_at"`
}

type UserPreferences struct {
	RoundupCategories []string    `json:"roundup_categories"` // things like "food", "clothes", "groceries"
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
	Merchant  string    `json:"merchant"`
}

type TransactionService struct {
	repo      TransactionRepository
	userRepo  UserRepository
	upiClient UPIClient
}

// TODO: select database to implement all these interfaces
type TransactionRepository interface {
	SaveTransaction(tx Transaction) error
	GetTransactionsByUserID(userID string) ([]Transaction, error)
	// Other methods as needed
}

type UserRepository interface {
	FindByID(id string) (*User, error)
	Update(user *User) error
	UpdatePreferences(userID string, prefs UserPreferences) error
	// Other methods as needed
}

type UPIClient interface {
	TransferFunds(userID string, amount float64) error
}

func main() {
	router := gin.Default()

	// public routes
	router.POST("/api/v1/auth/register", registerHandler)
	router.POST("/api/v1/auth/login", loginHandler)
	// protected routes
	authorized := router.Group("/api/v1")
	authorized.Use(authMiddleware())
	{
		authorized.GET("/transactions", getTransactionsHandler)
		authorized.POST("/connect-upi", connectUPIHandler)
		// TODO: more routes
	}

	router.Run(":1717")
}

// returns a gin middleware function for each request
func authMiddleware() gin.HandlerFunc {
	// gin.Context is basically a global variable shared by all the handlers and middleware
	return func(c *gin.Context) {
		// check the Authorization header
		token := c.GetHeader("Authorization")

		if token == "" {
			// send HTTP request back to client with error 401
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization required"})

			// stop handling this request
			c.Abort()
			return
		}

		claims, err := validateToken(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		// set the claims in context and move on the next request
		c.Set("userID", claims.UserID)
		c.Next()
	}
}

type CustomClaims struct {
	jwt.RegisteredClaims
	UserID string `json:"user_id"`
}

func validateToken(tokenString string) (*CustomClaims, error) {
	// Parse the token with CustomClaims
	token, err := jwt.ParseWithClaims(tokenString, &CustomClaims{},
		func(token *jwt.Token) (interface{}, error) {
			return []byte(os.Getenv("JWT_SECRET")), nil
		})

	if err != nil {
		return nil, err
	}

	// Check if token is nil or if an error occurred during parsing.
	if claims, ok := token.Claims.(*CustomClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token claims")
}

func loginHandler(c *gin.Context) {
	// TODO: Implement real login logic
}

func registerHandler(c *gin.Context) {
	// TODO: Implement user registration
}

func (s *TransactionService) ProcessRoundup(userID string, transaction Transaction) error {

	// find the user in userRepo to get their preferences
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return fmt.Errorf("User not found: %v", err)
	}

	// if the transaction category is not present in the users pref category, return nill
	if !contains(user.Preferences.RoundupCategories, transaction.Category) {
		return nil
	}

	baseRoundup := slabBasedRoundup(transaction.Amount*(1+BaseRoundupPercent)) - transaction.Amount

	daysRemaining := math.Floor(user.Preferences.TargetDate.Sub(time.Now()).Hours() / 24)
	// minimum 1 day
	daysRemaining = math.Max(1, daysRemaining)

	remainingAmount := user.Preferences.GoalAmount - user.Preferences.CurrentSavings
	requiredTxns := math.Floor(remainingAmount / user.Preferences.AverageRoundup)

	// fefine the period for recent transactions
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
		// TODO:
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

	transaction.Roundup = Roundup

	// add to the transaction repo
	if err := s.repo.SaveTransaction(transaction); err != nil {
		return fmt.Errorf("failed to update transaction: %v", err)
	}

	// TODO: UPI / Saving to wallet goes here

	// update user pref
	user.Preferences.CurrentSavings += Roundup
	user.Preferences.RoundupHistory = append(user.Preferences.RoundupHistory, Roundup)
	user.Preferences.RoundupDates = append(user.Preferences.RoundupDates, time.Now())

	// Save pref to user repo
	if err := s.userRepo.UpdatePreferences(userID, user.Preferences); err != nil {
		return fmt.Errorf("failed to update user preferences: %v", err)
	}

	return nil
}

func slabBasedRoundup(amount float64) float64 {
	switch {
	case amount <= 50:
		return math.Ceil(amount/5) * 5
	case amount <= 200:
		return math.Ceil(amount/10) * 10
	case amount <= 500:
		return math.Ceil(amount/50) * 50
	default:
		return math.Ceil(amount/100) * 100
	}
}

func contains(categories []string, target string) bool {
	for _, cat := range categories {
		if strings.EqualFold(cat, target) {
			return true
		}
	}
	return false
}

func getTransactionsHandler(c *gin.Context) {

	// get userID from gin Context
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "userID not found"})
		return
	}

	// check if it is of correct format
	uid, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid userID type"})
		return
	}

	transactions, err := txnService.repo.GetTransactionsByUserID(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, g.H{"error": "failed to retrieve transactions"})
		return
	}

	c.JSON(http.StatusOK, transactions)
}

// TODO: Implement data access layer
