package apple

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dental-teklif/goth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/jwk"
	"golang.org/x/oauth2"
)

const (
	idTokenVerificationKeyEndpoint = "https://appleid.apple.com/auth/keys"
)

type ID struct {
	Sub            string `json:"sub"`
	Email          string `json:"email"`
	IsPrivateEmail bool   `json:"is_private_email"`
	EmailVerified  bool   `json:"email_verified"`
}

type Session struct {
	AuthURL      string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	ID
}

func (s Session) GetAuthURL() (string, error) {
	if s.AuthURL == "" {
		return "", errors.New(goth.NoAuthUrlErrorMessage)
	}
	return s.AuthURL, nil
}

func (s Session) Marshal() string {
	b, _ := json.Marshal(s)
	return string(b)
}

type IDTokenClaims struct {
	jwt.RegisteredClaims
	AccessTokenHash string     `json:"at_hash"`
	AuthTime        int        `json:"auth_time"`
	Email           string     `json:"email"`
	IsPrivateEmail  BoolString `json:"is_private_email"`
	EmailVerified   BoolString `json:"email_verified,omitempty"`
}

func (s *Session) Authorize(provider goth.Provider, params goth.Params) (string, error) {
	p := provider.(*Provider)
	opts := []oauth2.AuthCodeOption{
		// Apple requires client id & secret as headers
		oauth2.SetAuthURLParam("client_id", p.clientId),
		oauth2.SetAuthURLParam("client_secret", p.secret),
	}
	token, err := p.config.Exchange(context.Background(), params.Get("code"), opts...)
	if err != nil {
		return "", err
	}

	if !token.Valid() {
		return "", errors.New("invalid token received from provider")
	}

	s.AccessToken = token.AccessToken
	s.RefreshToken = token.RefreshToken
	s.ExpiresAt = token.Expiry

	if idToken := token.Extra("id_token"); idToken != nil {
		idToken, err := jwt.ParseWithClaims(idToken.(string), &IDTokenClaims{}, func(t *jwt.Token) (interface{}, error) {
			kid := t.Header["kid"].(string)
			claims := t.Claims.(*IDTokenClaims)
			validator := jwt.NewValidator(jwt.WithAudience(p.clientId), jwt.WithIssuer(AppleAudOrIss))
			err := validator.Validate(claims)
			if err != nil {
				return nil, err
			}

			// per OpenID Connect Core 1.0 §3.2.2.9, Access Token Validation
			hash := sha256.Sum256([]byte(s.AccessToken))
			halfHash := hash[0:(len(hash) / 2)]
			encodedHalfHash := base64.RawURLEncoding.EncodeToString(halfHash)
			if encodedHalfHash != claims.AccessTokenHash {
				return nil, fmt.Errorf(`identity token invalid`)
			}

			// get the public key for verifying the identity token signature
			set, err := jwk.Fetch(context.Background(), idTokenVerificationKeyEndpoint, jwk.WithHTTPClient(p.Client()))
			if err != nil {
				return nil, err
			}
			selectedKey, found := set.LookupKeyID(kid)
			if !found {
				return nil, errors.New("could not find matching public key")
			}
			pubKey := &rsa.PublicKey{}
			err = selectedKey.Raw(pubKey)
			if err != nil {
				return nil, err
			}
			return pubKey, nil
		})
		if err != nil {
			return "", err
		}
		s.ID = ID{
			Sub:            idToken.Claims.(*IDTokenClaims).Subject,
			Email:          idToken.Claims.(*IDTokenClaims).Email,
			IsPrivateEmail: idToken.Claims.(*IDTokenClaims).IsPrivateEmail.Value(),
			EmailVerified:  idToken.Claims.(*IDTokenClaims).EmailVerified.Value(),
		}
	}

	return token.AccessToken, err
}

func (s Session) String() string {
	return s.Marshal()
}

// BoolString is a type that can be unmarshalled from a JSON field that can be either a boolean or a string.
// It is used to unmarshal some fields in the Apple ID token that can be sent as either boolean or string.
// See https://developer.apple.com/documentation/sign_in_with_apple/sign_in_with_apple_rest_api/authenticating_users_with_sign_in_with_apple#3383773
type BoolString struct {
	BoolValue   bool
	StringValue string
	IsValidBool bool
}

func (bs *BoolString) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		bs.BoolValue = b
		bs.IsValidBool = true
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		bs.StringValue = s
		return nil
	}

	return errors.New("json field can be either boolean or string")
}

func (bs *BoolString) Value() bool {
	if bs.IsValidBool {
		return bs.BoolValue
	}
	return bs.StringValue == "true"
}
