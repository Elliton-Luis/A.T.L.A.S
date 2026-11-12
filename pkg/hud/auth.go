package hud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type TelegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// ValidateTelegramWebAppData verifies the cryptographic signature of Telegram WebApp initData.
func ValidateTelegramWebAppData(initData, botToken string, allowedChatID int64) bool {
	if initData == "" || botToken == "" {
		return false
	}

	values, err := url.ParseQuery(initData)
	if err != nil {
		return false
	}

	expectedHash := values.Get("hash")
	if expectedHash == "" {
		return false
	}

	// 1. Sort all keys alphabetically except 'hash'
	var keys []string
	for k := range values {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var dataCheckArr []string
	for _, k := range keys {
		dataCheckArr = append(dataCheckArr, fmt.Sprintf("%s=%s", k, values.Get(k)))
	}
	dataCheckString := strings.Join(dataCheckArr, "\n")

	// 2. secret_key = HMAC_SHA256("WebAppData", botToken)
	macSecret := hmac.New(sha256.New, []byte("WebAppData"))
	macSecret.Write([]byte(botToken))
	secretKey := macSecret.Sum(nil)

	// 3. calculated_hash = HMAC_SHA256(secretKey, dataCheckString)
	macHash := hmac.New(sha256.New, secretKey)
	macHash.Write([]byte(dataCheckString))
	calculatedHash := hex.EncodeToString(macHash.Sum(nil))

	if !hmac.Equal([]byte(calculatedHash), []byte(expectedHash)) {
		return false
	}

	// 4. Verify user ID matches allowed Chat ID (Whitelist)
	if allowedChatID != 0 {
		userJSON := values.Get("user")
		if userJSON == "" {
			return false
		}
		var user TelegramUser
		if err := json.Unmarshal([]byte(userJSON), &user); err != nil {
			return false
		}
		if user.ID != allowedChatID {
			return false
		}
	}

	return true
}
