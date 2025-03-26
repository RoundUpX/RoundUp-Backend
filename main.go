package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"database/sql"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// define magic numbers
// TODO: Decide all these!!!
const BaseRoundupPercent = 0.05
const RecentPeriodDays = 7
const MinPressure = 0.5
const MaxPressure = 5
const DefaultAvgTxnsPerDay = 2

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
	UpdatePreferences(userID string, prefs UserPreferences) error
	CreateUser(user *User) error
	GetUserByEmail(email string) (*User, error)
}

type UPIClient interface {
	GenerateUPIURI(fromUserID, toAccount string, amount float64) (string, error)
}

var txnService *TransactionService

func main() {

	db, err := connectDB()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	txRepo := &PostgresTransactionRepository{db: db}
	userRepo := &PostgresUserRepository{db: db}
	UPIclient := &DummyUPIClient{}

	txnService = &TransactionService{
		repo:      txRepo,
		userRepo:  userRepo,
		upiClient: UPIclient,
	}

	router := gin.Default()

	// public routes
	router.POST("/api/v1/auth/register", registerHandler)
	router.POST("/api/v1/auth/login", loginHandler)
	// protected routes
	authorized := router.Group("/api/v1")
	authorized.Use(authMiddleware())
	{
		authorized.GET("/transactions", getTransactionsHandler)
		authorized.POST("/transaction", addTransactionHandler)
		authorized.GET("/transactions/:id", getTransactionByIDHandler)
		// authorized.POST("/connect-upi", connectUPIHandler)
		// TODO: UPI
	}

	router.Run(":8082")
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
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	err := c.BindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	user, err := txnService.userRepo.GetUserByEmail(req.Email)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
		return
	}

	claims := CustomClaims{
		UserID: user.ID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(72 * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(os.Getenv("JWT_SECRET")))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
	}

	c.JSON(http.StatusOK, gin.H{"token": tokenString})
}

func registerHandler(c *gin.Context) {
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	err := c.BindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	if req.Name == "" || req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name, email, and password are required"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process password"})
		return
	}

	newUser := User{
		ID:        uuid.New().String(),
		Name:      req.Name,
		Email:     req.Email,
		Password:  string(hashedPassword),
		CreatedAt: time.Now(),
	}

	err = txnService.userRepo.CreateUser(&newUser)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to register user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "User registered successfully"})
}

func (s *TransactionService) ProcessRoundup(userID string, transaction Transaction) (string, string, error) {

	// find the user in userRepo to get their preferences
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return "", "", fmt.Errorf("User not found: %v", err)
	}

	// if the transaction category is not present in the users pref category, return nill
	if !contains(user.Preferences.RoundupCategories, transaction.Category) {
		return "", "", nil
	}

	baseRoundup := slabBasedRoundup(transaction.Amount*(1+BaseRoundupPercent)) - transaction.Amount

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

	transaction.Roundup = Roundup

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

	merchantURI, err := s.upiClient.GenerateUPIURI(userID, transaction.Merchant, transaction.Amount)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate UPI URI for merchant: %v", err)
	}

	roundupURI, err := s.upiClient.GenerateUPIURI(userID, roundUpAccount, transaction.Amount)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate UPI URI for RoundUp: %v", err)
	}

	return merchantURI, roundupURI, nil
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve transactions"})
		return
	}

	c.JSON(http.StatusOK, transactions)
}

// Data access layer

func connectDB() (*sql.DB, error) {
	connStr := "user=roundup_user password=roundup123 dbname=roundup sslmode=disable"
	db, err := sql.Open("postgres", connStr)

	if err != nil {
		return nil, err
	}

	err = db.Ping()
	if err != nil {
		log.Fatal(err)
	}

	return db, nil
}

type PostgresTransactionRepository struct {
	db *sql.DB
}

func (r *PostgresTransactionRepository) SaveTransaction(tx Transaction) error {

	query := "INSERT INTO transactions (id, user_id, amount, category, roundup, created_at, merchant) VALUES ($1, $2, $3, $4, $5, $6, $7)"
	_, err := r.db.Exec(query, tx.ID, tx.UserID, tx.Amount, tx.Category, tx.Roundup, tx.CreatedAt, tx.Merchant)
	return err
}

func (r *PostgresTransactionRepository) GetTransactionsByUserID(userID string) ([]Transaction, error) {

	query := "SELECT id, user_id, amount, category, roundup, created_at, merchant FROM transactions WHERE user_id = $1"
	rows, err := r.db.Query(query, userID)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var transactions []Transaction
	for rows.Next() {
		var tx Transaction
		err := rows.Scan(&tx.ID, &tx.UserID, &tx.Amount, &tx.Category, &tx.Roundup, &tx.CreatedAt, &tx.Merchant)

		if err != nil {
			return nil, err
		}

		transactions = append(transactions, tx)
	}

	return transactions, nil
}

func (r *PostgresTransactionRepository) GetTransactionByID(id string) (*Transaction, error) {
	query := "SELECT id, user_id, amount, category, roundup, created_at, merchant FROM transactions WHERE id = $1"

	var tx Transaction
	err := r.db.QueryRow(query, id).Scan(&tx.ID, &tx.UserID, &tx.Amount, &tx.Category, &tx.Roundup, &tx.CreatedAt, &tx.Merchant)

	if err != nil {
		return nil, err
	}

	return &tx, nil
}

type PostgresUserRepository struct {
	db *sql.DB
}

func (r *PostgresUserRepository) FindByID(id string) (*User, error) {

	var user User
	query := "SELECT id, name, email, created_at FROM users WHERE id = $1"

	err := r.db.QueryRow(query, id).Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)

	if err != nil {
		return nil, err
	}

	// Fetch user preferences separately
	query = "SELECT roundup_categories, goal_amount, target_date, current_savings, average_roundup FROM user_preferences WHERE user_id = $1"
	err = r.db.QueryRow(query, id).Scan(
		&user.Preferences.RoundupCategories,
		&user.Preferences.GoalAmount,
		&user.Preferences.TargetDate,
		&user.Preferences.CurrentSavings,
		&user.Preferences.AverageRoundup,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *PostgresUserRepository) UpdatePreferences(userID string, prefs UserPreferences) error {

	query := "UPDATE user_preferences SET roundup_categories = $1, goal_amount = $2, target_date = $3, current_savings = $4, average_roundup = $5 WHERE user_id = $6"
	_, err := r.db.Exec(query, prefs.RoundupCategories, prefs.GoalAmount, prefs.TargetDate, prefs.CurrentSavings, prefs.AverageRoundup, userID)
	return err
}

// Add this method to your PostgresUserRepository struct
func (r *PostgresUserRepository) Update(user *User) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}

	// Update basic user info
	_, err = tx.Exec("UPDATE users SET name = $1, email = $2 WHERE id = $3",
		user.Name, user.Email, user.ID)
	if err != nil {
		tx.Rollback()
		return err
	}

	// Update user preferences
	err = r.updatePreferencesInTx(tx, user.ID, user.Preferences)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// Helper method for transaction-based preference updates
func (r *PostgresUserRepository) updatePreferencesInTx(tx *sql.Tx, userID string, prefs UserPreferences) error {

	query := "UPDATE user_preferences SET roundup_categories = $1, goal_amount = $2, target_date = $3, current_savings = $4, average_roundup = $5, roundup_history = $6, roundup_dates = $7 WHERE user_id = $8"

	_, err := tx.Exec(query,
		pq.Array(prefs.RoundupCategories),
		prefs.GoalAmount,
		prefs.TargetDate,
		prefs.CurrentSavings,
		prefs.AverageRoundup,
		pq.Array(prefs.RoundupHistory),
		pq.Array(prefs.RoundupDates),
		userID)
	return err
}

func (r *PostgresUserRepository) CreateUser(user *User) error {
	query := "INSERT INTO users (id, name, email, password, created_at) VALUES ($1, $2, $3, $4, $5)"
	_, err := r.db.Exec(query, user.ID, user.Name, user.Email, user.Password, user.CreatedAt)
	return err
}

func (r *PostgresUserRepository) GetUserByEmail(email string) (*User, error) {
	var user User
	query := "SELECT id, name, email, password, created_at FROM users WHERE email = $1"
	err := r.db.QueryRow(query, email).Scan(&user.ID, &user.Name, &user.Email, &user.Password, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func addTransactionHandler(c *gin.Context) {

	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	uid, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid userID format"})
		return
	}

	var txn Transaction
	err := c.BindJSON(&txn)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid transaction input"})
		return
	}

	txn.UserID = uid
	txn.ID = uuid.New().String()
	txn.CreatedAt = time.Now()

	merchantURI, roundupURI, err := txnService.ProcessRoundup(uid, txn)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	response := gin.H{
		"message":      "Transaction added successfully",
		"transaction":  txn,
		"merchant_uri": merchantURI,
		"roundup_uri":  roundupURI,
	}

	c.JSON(http.StatusOK, response)
}

func getTransactionByIDHandler(c *gin.Context) {

	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	uid, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid user ID format"})
		return
	}

	// Get the transaction ID from the URL parameter
	txID := c.Param("id")
	if txID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Transaction ID is required"})
		return
	}

	tx, err := txnService.repo.GetTransactionByID(txID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Transaction not found"})
		return
	}

	if tx.UserID != uid {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	c.JSON(http.StatusOK, tx)
}

type DummyUPIClient struct{}

func (d *DummyUPIClient) GenerateUPIURI(fromUserID, toAccount string, amount float64) (string, error) {

	upiURI := fmt.Sprintf("upi://pay?pa=%s&pn=%s&am=%.2f&cu=INR", toAccount, fromUserID, amount)
	return upiURI, nil
}
