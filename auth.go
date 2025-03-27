package main

import (
	"errors"
	"os"

	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

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
		return nil, err
	}

	// Check if token is nil or if an error occurred during parsing.
	if claims, ok := token.Claims.(*CustomClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token claims")
}
