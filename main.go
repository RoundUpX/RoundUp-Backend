package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
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

// Global variable
var txnService *TransactionService

// main function
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

		authorized.GET("/preferences", getPreferencesHandler)
		authorized.PUT("/preferences", updatePreferencesHandler)

		authorized.GET("/preferences/goal", getGoalHandler)
		authorized.POST("/preferences/goal", addGoalHandler)
		authorized.PUT("/preferences/goal", changeGoalHandler)
	}

	router.Run(":8082")
}

// Database connection
func connectDB() (*sql.DB, error) {
	connStr := "user=roundup_user password=roundup123 dbname=roundup sslmode=disable"
	db, err := sql.Open("postgres", connStr)

	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	err = db.Ping()
	if err != nil {
		fmt.Println(err)
		log.Fatal(err)
	}

	return db, nil
}

// PostgresTransactionRepository and its methods
type PostgresTransactionRepository struct {
	db *sql.DB
}

func (r *PostgresTransactionRepository) SaveTransaction(tx Transaction) error {

	query := "INSERT INTO transactions (id, user_id, amount, category, roundup, created_at, merchant) VALUES ($1, $2, $3, $4, $5, $6, $7)"
	_, err := r.db.Exec(query, tx.ID, tx.UserID, tx.Amount, tx.Category, tx.Roundup, tx.CreatedAt, tx.Merchant)
	fmt.Println(err)
	return err
}

func (r *PostgresTransactionRepository) GetTransactionsByUserID(userID string) ([]Transaction, error) {

	query := "SELECT id, user_id, amount, category, roundup, created_at, merchant FROM transactions WHERE user_id = $1"
	rows, err := r.db.Query(query, userID)

	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	defer rows.Close()

	var transactions []Transaction
	for rows.Next() {
		var tx Transaction
		err := rows.Scan(&tx.ID, &tx.UserID, &tx.Amount, &tx.Category, &tx.Roundup, &tx.CreatedAt, &tx.Merchant)

		if err != nil {
			fmt.Println(err)
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
		fmt.Println(err)
		return nil, err
	}

	return &tx, nil
}

// PostgresUserRepository and its methods
type PostgresUserRepository struct {
	db *sql.DB
}

func (r *PostgresUserRepository) FindByID(id string) (*User, error) {

	var user User
	query := "SELECT id, name, email, created_at FROM users WHERE id = $1"

	err := r.db.QueryRow(query, id).Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)

	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	// Fetch user preferences separately
	query = "SELECT roundup_categories, goal_name, goal_amount, target_date, current_savings, average_roundup FROM user_preferences WHERE user_id = $1"
	err = r.db.QueryRow(query, id).Scan(
		pq.Array(&user.Preferences.RoundupCategories),
		&user.Preferences.GoalName,
		&user.Preferences.GoalAmount,
		&user.Preferences.TargetDate,
		&user.Preferences.CurrentSavings,
		&user.Preferences.AverageRoundup,
	)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	return &user, nil
}

func (r *PostgresUserRepository) CreateUserPreferences(userID string, prefs UserPreferences) error {
	query := `
		INSERT INTO user_preferences
		(user_id, roundup_categories, goal_amount, target_date, current_savings, average_roundup, roundup_history, roundup_dates)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.db.Exec(query,
		userID,
		pq.Array(prefs.RoundupCategories),
		prefs.GoalAmount,
		prefs.TargetDate,
		prefs.CurrentSavings,
		prefs.AverageRoundup,
		pq.Array(prefs.RoundupHistory),
		pq.Array(prefs.RoundupDates),
	)
	return err
}

func (r *PostgresUserRepository) UpdatePreferences(userID string, prefs UserPreferences) error {

	query := "UPDATE user_preferences SET roundup_categories = $1, goal_amount = $2, target_date = $3, current_savings = $4, average_roundup = $5 WHERE user_id = $6"
	_, err := r.db.Exec(query, pq.Array(prefs.RoundupCategories), prefs.GoalAmount, prefs.TargetDate, prefs.CurrentSavings, prefs.AverageRoundup, userID)
	fmt.Println(err)
	return err
}

func (r *PostgresUserRepository) Update(user *User) error {
	tx, err := r.db.Begin()
	if err != nil {
		fmt.Println(err)
		return err
	}

	// Update basic user info
	_, err = tx.Exec("UPDATE users SET name = $1, email = $2 WHERE id = $3",
		user.Name, user.Email, user.ID)
	if err != nil {
		tx.Rollback()
		fmt.Println(err)
		return err
	}

	// Update user preferences
	err = r.updatePreferences(tx, user.ID, user.Preferences)
	if err != nil {
		tx.Rollback()
		fmt.Println(err)
		return err
	}

	return tx.Commit()
}

func (r *PostgresUserRepository) updatePreferences(tx *sql.Tx, userID string, prefs UserPreferences) error {

	query := "UPDATE user_preferences SET roundup_categories = $1, goal_name = $9, goal_amount = $2, target_date = $3, current_savings = $4, average_roundup = $5, roundup_history = $6, roundup_dates = $7 WHERE user_id = $8"

	_, err := tx.Exec(query,
		pq.Array(prefs.RoundupCategories),
		prefs.GoalAmount,
		prefs.TargetDate,
		prefs.CurrentSavings,
		prefs.AverageRoundup,
		pq.Array(prefs.RoundupHistory),
		pq.Array(prefs.RoundupDates),
		userID,
		prefs.GoalName,
	)
	fmt.Println(err)
	return err
}

func (r *PostgresUserRepository) CreateUser(user *User) error {
	query := "INSERT INTO users (id, name, email, password, created_at) VALUES ($1, $2, $3, $4, $5)"
	_, err := r.db.Exec(query, user.ID, user.Name, user.Email, user.Password, user.CreatedAt)
	fmt.Println(err)
	return err
}

func (r *PostgresUserRepository) GetUserByEmail(email string) (*User, error) {
	var user User
	query := "SELECT id, name, email, password, created_at FROM users WHERE email = $1"
	err := r.db.QueryRow(query, email).Scan(&user.ID, &user.Name, &user.Email, &user.Password, &user.CreatedAt)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	return &user, nil
}

// Authentication and middleware
type CustomClaims struct {
	jwt.RegisteredClaims
	UserID string `json:"user_id"`
}

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

func validateToken(tokenString string) (*CustomClaims, error) {
	// Parse the token with CustomClaims
	token, err := jwt.ParseWithClaims(tokenString, &CustomClaims{},
		func(token *jwt.Token) (interface{}, error) {
			return []byte(os.Getenv("JWT_SECRET")), nil
		})

	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	// Check if token is nil or if an error occurred during parsing.
	if claims, ok := token.Claims.(*CustomClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token claims")
}

// Handlers
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

	// Insert default pref for the new user
	defaultPrefs := UserPreferences{
		RoundupCategories: []string{},
		GoalName:          "",
		GoalAmount:        0,
		TargetDate:        time.Time{},
		CurrentSavings:    0,
		AverageRoundup:    0,
		RoundupHistory:    []float64{},
		RoundupDates:      []time.Time{},
	}

	err = txnService.userRepo.CreateUserPreferences(newUser.ID, defaultPrefs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user preferences"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "User registered successfully"})
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
		fmt.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve transactions"})
		return
	}

	c.JSON(http.StatusOK, transactions)
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

func getPreferencesHandler(c *gin.Context) {

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

	user, err := txnService.userRepo.FindByID(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve user preferences"})
		return
	}

	c.JSON(http.StatusOK, user.Preferences)
}

func updatePreferencesHandler(c *gin.Context) {
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

	var newPrefs UserPreferences
	err := c.BindJSON(&newPrefs)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
	}

	err = txnService.userRepo.UpdatePreferences(uid, newPrefs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user preferences"})
	}

	c.JSON(http.StatusOK, gin.H{"message": "User preferences updates successfully"})
}

func getGoalHandler(c *gin.Context) {
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

	user, err := txnService.userRepo.FindByID(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find user"})
		return
	}

	type Goal struct {
		Name   string    `json:"name"`
		Amount float64   `json:"amount"`
		Date   time.Time `json:"date"`
	}

	var goal Goal

	goal.Amount = user.Preferences.GoalAmount
	goal.Date = user.Preferences.TargetDate
	goal.Name = user.Preferences.GoalName

	c.JSON(http.StatusOK, goal)
}

func addGoalHandler(c *gin.Context) {

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

	var req struct {
		GoalName   string  `json:"name"`
		GoalAmount float64 `json:"amount"`
		TargetDate string  `json:"date"`
	}

	err := c.BindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	targetDate, err := time.Parse("2006-01-02", req.TargetDate)
	if err != nil {
		fmt.Println("Received TargetDate:", req.TargetDate)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format. Use YYYY-MM-DD"})
		return
	}

	user, err := txnService.userRepo.FindByID(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find user"})
		return
	}

	// TODO Multiple goals per user?
	if user.Preferences.GoalAmount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Goal already exists. Create PUT request to update it"})
		return
	}

	user.Preferences.GoalName = req.GoalName
	user.Preferences.GoalAmount = req.GoalAmount
	user.Preferences.TargetDate = targetDate

	err = txnService.userRepo.Update(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add goal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Goal added successfully"})
}
func changeGoalHandler(c *gin.Context) {

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

	var req struct {
		GoalName   string  `json:"goal_name"`
		GoalAmount float64 `json:"goal_amount"`
		TargetDate string  `json:"target_date"`
	}

	err := c.BindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	targetDate, err := time.Parse("2006-01-02", req.TargetDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format. Use YYYY-MM-DD"})
		return
	}

	user, err := txnService.userRepo.FindByID(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find user"})
		return
	}

	if user.Preferences.GoalAmount == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No goal set. Use addGoalHandler to create one."})
		return
	}

	user.Preferences.GoalName = req.GoalName
	user.Preferences.GoalAmount = req.GoalAmount
	user.Preferences.TargetDate = targetDate

	err = txnService.userRepo.Update(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update goal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Goal updated successfully"})
}

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

	fmt.Sprintf("%.2f", Roundup)

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
