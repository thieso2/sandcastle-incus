package authapp

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// oidcJWK is one usable signing key from the provider's JWKS.
type oidcJWK struct {
	KID string
	Alg string // "" when the JWK does not pin one
	EC  *ecdsa.PublicKey
	RSA *rsa.PublicKey
}

type oidcIDTokenClaims struct {
	Issuer        string          `json:"iss"`
	Subject       string          `json:"sub"`
	Audience      json.RawMessage `json:"aud"`
	AuthorizedBy  string          `json:"azp"`
	Expiry        json.Number     `json:"exp"`
	IssuedAt      json.Number     `json:"iat"`
	Nonce         string          `json:"nonce"`
	Email         string          `json:"email"`
	EmailVerified *bool           `json:"email_verified"`
	Name          string          `json:"name"`
}

func parseOIDCJWK(raw json.RawMessage) (oidcJWK, error) {
	var jwk struct {
		KTY string `json:"kty"`
		Use string `json:"use"`
		KID string `json:"kid"`
		Alg string `json:"alg"`
		CRV string `json:"crv"`
		X   string `json:"x"`
		Y   string `json:"y"`
		N   string `json:"n"`
		E   string `json:"e"`
	}
	if err := json.Unmarshal(raw, &jwk); err != nil {
		return oidcJWK{}, err
	}
	if jwk.Use != "" && jwk.Use != "sig" {
		return oidcJWK{}, fmt.Errorf("key %q is not a signing key", jwk.KID)
	}
	key := oidcJWK{KID: jwk.KID, Alg: jwk.Alg}
	switch jwk.KTY {
	case "EC":
		if jwk.CRV != "P-256" {
			return oidcJWK{}, fmt.Errorf("unsupported curve %q", jwk.CRV)
		}
		x, errX := base64.RawURLEncoding.DecodeString(jwk.X)
		y, errY := base64.RawURLEncoding.DecodeString(jwk.Y)
		if errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
			return oidcJWK{}, fmt.Errorf("invalid EC key %q", jwk.KID)
		}
		public, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
		if err != nil {
			return oidcJWK{}, err
		}
		key.EC = public
	case "RSA":
		n, errN := base64.RawURLEncoding.DecodeString(jwk.N)
		e, errE := base64.RawURLEncoding.DecodeString(jwk.E)
		if errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			return oidcJWK{}, fmt.Errorf("invalid RSA key %q", jwk.KID)
		}
		public := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if public.N.BitLen() < 2048 {
			return oidcJWK{}, fmt.Errorf("RSA key %q is shorter than 2048 bits", jwk.KID)
		}
		key.RSA = public
	default:
		return oidcJWK{}, fmt.Errorf("unsupported key type %q", jwk.KTY)
	}
	return key, nil
}

// verifyIDToken checks the ID token's signature against the provider's JWKS
// (ES256 or RS256) and its iss, aud/azp, exp, iat, nonce and sub claims.
func (o *OIDCLogin) verifyIDToken(ctx context.Context, endpoints *oidcEndpoints, raw string, nonce string, now time.Time) (oidcIDTokenClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return oidcIDTokenClaims{}, fmt.Errorf("malformed token")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return oidcIDTokenClaims{}, fmt.Errorf("malformed header")
	}
	var header struct {
		Alg string `json:"alg"`
		KID string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return oidcIDTokenClaims{}, fmt.Errorf("malformed header")
	}
	if header.Alg != "ES256" && header.Alg != "RS256" {
		return oidcIDTokenClaims{}, fmt.Errorf("unsupported signing algorithm %q", header.Alg)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return oidcIDTokenClaims{}, fmt.Errorf("malformed signature")
	}
	keys, err := o.signingKeys(ctx, endpoints.JWKSURI, header.KID, now)
	if err != nil {
		return oidcIDTokenClaims{}, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !verifyJWSSignature(keys, header.Alg, digest[:], signature) {
		return oidcIDTokenClaims{}, fmt.Errorf("signature does not verify")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return oidcIDTokenClaims{}, fmt.Errorf("malformed payload")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	var claims oidcIDTokenClaims
	if err := decoder.Decode(&claims); err != nil {
		return oidcIDTokenClaims{}, fmt.Errorf("malformed payload")
	}
	if err := checkIDTokenClaims(claims, endpoints.Issuer, o.config.ClientID, nonce, now); err != nil {
		return oidcIDTokenClaims{}, err
	}
	return claims, nil
}

func verifyJWSSignature(keys []oidcJWK, alg string, digest []byte, signature []byte) bool {
	for _, key := range keys {
		if key.Alg != "" && key.Alg != alg {
			continue
		}
		switch {
		case alg == "ES256" && key.EC != nil:
			if len(signature) != 64 {
				return false
			}
			r := new(big.Int).SetBytes(signature[:32])
			s := new(big.Int).SetBytes(signature[32:])
			if ecdsa.Verify(key.EC, digest, r, s) {
				return true
			}
		case alg == "RS256" && key.RSA != nil:
			if rsa.VerifyPKCS1v15(key.RSA, crypto.SHA256, digest, signature) == nil {
				return true
			}
		}
	}
	return false
}

func checkIDTokenClaims(claims oidcIDTokenClaims, issuer string, clientID string, nonce string, now time.Time) error {
	if claims.Issuer != issuer {
		return fmt.Errorf("issuer %q, want %q", claims.Issuer, issuer)
	}
	audiences, err := parseAudience(claims.Audience)
	if err != nil {
		return err
	}
	if !containsString(audiences, clientID) {
		return fmt.Errorf("audience does not include this client")
	}
	if len(audiences) > 1 && claims.AuthorizedBy != clientID {
		return fmt.Errorf("authorized party %q, want %q", claims.AuthorizedBy, clientID)
	}
	expiry, err := claims.Expiry.Int64()
	if err != nil {
		return fmt.Errorf("exp is missing")
	}
	if !now.Before(time.Unix(expiry, 0).Add(oidcClockSkew)) {
		return fmt.Errorf("token expired")
	}
	issuedAt, err := claims.IssuedAt.Int64()
	if err != nil {
		return fmt.Errorf("iat is missing")
	}
	if time.Unix(issuedAt, 0).After(now.Add(oidcClockSkew)) {
		return fmt.Errorf("token issued in the future")
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return fmt.Errorf("nonce mismatch")
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return fmt.Errorf("sub is missing")
	}
	return nil
}

func parseAudience(raw json.RawMessage) ([]string, error) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	return nil, fmt.Errorf("aud is missing")
}
