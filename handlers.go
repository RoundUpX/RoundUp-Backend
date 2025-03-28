package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Handlers
func loginHandler(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
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
		Name     string `json:"name" binding:"required"`
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
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

	// Create wallet for the new user
	err = txnService.CreateUserWallet(newUser.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user wallet"})
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

	roundup, merchantURI, roundupURI, err := txnService.ProcessRoundup(uid, txn)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	txn.Roundup = roundup

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
		GoalName   string  `json:"name" binding:"required"`
		GoalAmount float64 `json:"amount" binding:"required"`
		TargetDate string  `json:"date" binding:"required"`
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
	// Retrieve the userID from the context
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

	// Define the request payload with required fields
	var req struct {
		GoalName   string  `json:"name" binding:"required"`
		GoalAmount float64 `json:"amount" binding:"required"`
		TargetDate string  `json:"date" binding:"required"`
	}

	// Bind and validate the JSON payload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input: " + err.Error()})
		return
	}

	// Parse the target date ensuring the format is YYYY-MM-DD
	targetDate, err := time.Parse("2006-01-02", req.TargetDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format. Use YYYY-MM-DD"})
		return
	}

	// Find the user by ID
	user, err := txnService.userRepo.FindByID(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find user"})
		return
	}

	// Check if a goal is already set (business logic)
	if user.Preferences.GoalAmount == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No goal set. Use addGoalHandler to create one."})
		return
	}

	// Update user preferences with the new goal data
	user.Preferences.GoalName = req.GoalName
	user.Preferences.GoalAmount = req.GoalAmount
	user.Preferences.TargetDate = targetDate

	// Persist the updated user record
	err = txnService.userRepo.Update(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update goal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Goal updated successfully"})
}

func getWalletBalanceHandler(c *gin.Context) {
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

	balance, err := txnService.GetWalletBalance(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get wallet balance: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"balance": balance})
}

func getWalletTransactionsHandler(c *gin.Context) {
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

	transactions, err := txnService.GetWalletTransactions(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get wallet transactions: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, transactions)
}

func addToWalletHandler(c *gin.Context) {
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
		Amount      float64 `json:"amount" binding:"required"`
		Description string  `json:"description"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input: " + err.Error()})
		return
	}

	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Amount must be positive"})
		return
	}

	err := txnService.AddToWallet(uid, req.Amount, req.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add to wallet: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Amount added to wallet successfully"})
}

func withdrawFromWalletHandler(c *gin.Context) {
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
		Amount      float64 `json:"amount" binding:"required"`
		Description string  `json:"description"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input: " + err.Error()})
		return
	}

	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Amount must be positive"})
		return
	}

	err := txnService.WithdrawFromWallet(uid, req.Amount, req.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to withdraw from wallet: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Amount withdrawn from wallet successfully"})
}
