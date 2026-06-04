package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RequestDeviceCode POSTs to AuthURL and returns the challenge.
func RequestDeviceCode(ctx context.Context, cfg Config) (DeviceCodeChallenge, error) {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	if cfg.Scope != "" {
		form.Set("scope", cfg.Scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.AuthURL(),
		strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceCodeChallenge{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return DeviceCodeChallenge{}, fmt.Errorf("device auth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return DeviceCodeChallenge{}, fmt.Errorf("device auth: status %d", resp.StatusCode)
	}
	var ch DeviceCodeChallenge
	if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
		return DeviceCodeChallenge{}, fmt.Errorf("decode device auth: %w", err)
	}
	ch.RetrievedAt = time.Now()
	if ch.VerificationURIComplete == "" {
		// Some implementations only return VerificationURI + UserCode
		ch.VerificationURIComplete = ch.VerificationURI
	}
	return ch, nil
}

const grantTypeDeviceCode = "urn:ietf:params:oauth:grant-type:device_code"

type tokenErr struct {
	Code string `json:"error"`
	Desc string `json:"error_description"`
}

// PollToken polls TokenURL at challenge.Interval until success, expiry,
// or ctx is cancelled.
func PollToken(ctx context.Context, cfg Config, ch DeviceCodeChallenge) (Token, error) {
	interval := ch.PollInterval()
	deadline := ch.ExpiresAt()

	for {
		now := time.Now()
		if now.After(deadline) {
			return Token{}, errors.New("device code expired")
		}
		tok, errCode, err := tokenOnce(ctx, cfg, ch.DeviceCode)
		if err != nil {
			return Token{}, err
		}
		switch errCode {
		case "":
			return tok, nil
		case "authorization_pending":
			// keep polling
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return Token{}, errors.New("user denied authorization")
		case "expired_token":
			return Token{}, errors.New("device code expired")
		case "invalid_grant":
			// agentserver's device-flow wrapper has a 3-step UX: (1) user
			// verifies user_code in the standard Hydra confirm page, (2) user
			// selects a workspace to join (extension on top of RFC 8628), only
			// then is the device_code marked approved. Between steps 1 and 2,
			// the token endpoint returns invalid_grant instead of the spec-
			// mandated authorization_pending. Treat as pending so we keep
			// polling until either the workspace is chosen (token succeeds)
			// or the device_code expires (real error surfaces as expired_token
			// or the deadline check above triggers).
		default:
			return Token{}, fmt.Errorf("oauth token error: %s", errCode)
		}
		select {
		case <-ctx.Done():
			return Token{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func tokenOnce(ctx context.Context, cfg Config, deviceCode string) (Token, string, error) {
	form := url.Values{}
	form.Set("grant_type", grantTypeDeviceCode)
	form.Set("client_id", cfg.ClientID)
	form.Set("device_code", deviceCode)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL(),
		strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Token{}, "", fmt.Errorf("token poll: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var tok Token
		if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
			return Token{}, "", err
		}
		return tok, "", nil
	}
	// 400 with JSON {error: "..."} is normal device-flow signalling
	var te tokenErr
	if err := json.NewDecoder(resp.Body).Decode(&te); err != nil {
		return Token{}, "", fmt.Errorf("token poll: status %d", resp.StatusCode)
	}
	if te.Code == "" {
		return Token{}, "", fmt.Errorf("token poll: status %d (no error code)", resp.StatusCode)
	}
	return Token{}, te.Code, nil
}
