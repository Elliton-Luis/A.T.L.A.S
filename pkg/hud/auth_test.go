package hud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"testing"
)

func TestValidateTelegramWebAppData(t *testing.T) {
	botToken := "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
	var allowedChatID int64 = 987654321

	userJSON := fmt.Sprintf(`{"id":%d,"first_name":"Elliton","username":"elliton"}`, allowedChatID)

	// Build raw query string
	params := url.Values{}
	params.Set("auth_date", "1700000000")
	params.Set("query_id", "AAG_test")
	params.Set("user", userJSON)

	// Calculate correct hash
	dataCheckString := fmt.Sprintf("auth_date=1700000000\nquery_id=AAG_test\nuser=%s", userJSON)

	macSecret := hmac.New(sha256.New, []byte("WebAppData"))
	macSecret.Write([]byte(botToken))
	secretKey := macSecret.Sum(nil)

	macHash := hmac.New(sha256.New, secretKey)
	macHash.Write([]byte(dataCheckString))
	correctHash := hex.EncodeToString(macHash.Sum(nil))

	params.Set("hash", correctHash)
	validInitData := params.Encode()

	// 1. Test Valid InitData
	if !ValidateTelegramWebAppData(validInitData, botToken, allowedChatID) {
		t.Errorf("expected valid initData to pass validation")
	}

	// 2. Test Invalid Hash
	params.Set("hash", "deadbeef0000111122223333444455556666777788889999aaaabbbbccccdddd")
	invalidInitData := params.Encode()
	if ValidateTelegramWebAppData(invalidInitData, botToken, allowedChatID) {
		t.Errorf("expected invalid hash to be rejected")
	}

	// 3. Test Unauthorized User ID
	params.Set("hash", correctHash)
	var wrongChatID int64 = 111222333
	if ValidateTelegramWebAppData(validInitData, botToken, wrongChatID) {
		t.Errorf("expected unauthorized user ID to be rejected")
	}
}
