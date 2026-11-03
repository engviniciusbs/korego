package auth

import (
	"crypto/subtle"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const totpPeriod = 30

// NewTOTPSecret generates a TOTP secret and its otpauth URL.
func NewTOTPSecret(email string) (secret, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "KoreGO",
		AccountName: email,
		Period:      totpPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// CheckTOTP validates code against steps now-1..now+1. It returns the matched
// step; matches with step <= lastStep are rejected (anti-replay).
func CheckTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	cur := now.Unix() / totpPeriod
	opts := totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	var found int64
	ok := false
	for step := cur - 1; step <= cur+1; step++ {
		want, err := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0), opts)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 && !ok {
			found, ok = step, true
		}
	}
	if !ok || found <= lastStep {
		return 0, false
	}
	return found, true
}
