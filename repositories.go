package main

import (
	"database/sql"
	"fmt"

	"github.com/lib/pq"
)

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
