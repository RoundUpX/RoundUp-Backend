package main

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

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
	walletRepo := &PostgresWalletRepository{db: db}

	txnService = &TransactionService{
		repo:       txRepo,
		userRepo:   userRepo,
		upiClient:  UPIclient,
		walletRepo: walletRepo,
	}

	router := gin.Default()

	// public routes
	router.POST("/api/v1/auth/register", registerHandler)
	router.POST("/api/v1/auth/login", loginHandler)
	router.POST("/api/v1/upi/verify", verifyUPIHandler)
	router.POST("/api/v1/transaction/type", getTransactionTypeHandler)

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

		authorized.GET("/wallet/balance", getWalletBalanceHandler)
		authorized.GET("/wallet/transactions", getWalletTransactionsHandler)
		authorized.POST("/wallet/add", addToWalletHandler)
		authorized.POST("/wallet/withdraw", withdrawFromWalletHandler)

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
