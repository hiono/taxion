package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"golang.org/x/oauth2"
)

type fakeOIDCOptions struct {
	wrongState     bool
	wrongSignature bool
	mutateClaims   func(map[string]any)
}

func TestAuthenticateUsesAuthorizationCodePKCEAndNonce(t *testing.T) {
	principal, output, err := runFakeOIDC(t, fakeOIDCOptions{})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if principal.Subject() != "subject-123" {
		t.Fatalf("Subject() = %q, want subject-123", principal.Subject())
	}
	if principal.PrincipalID() == "" {
		t.Fatal("PrincipalID() is empty")
	}
	if strings.Contains(output, "access-token-secret") || strings.Contains(output, "id-token-secret") {
		t.Fatalf("authentication output exposed a token: %q", output)
	}
}

func TestAuthenticateRejectsInvalidIDTokens(t *testing.T) {
	cases := []struct {
		name           string
		mutateClaims   func(map[string]any)
		wrongSignature bool
	}{
		{
			name: "nonce mismatch",
			mutateClaims: func(claims map[string]any) {
				claims["nonce"] = "wrong-nonce"
			},
		},
		{
			name: "audience mismatch",
			mutateClaims: func(claims map[string]any) {
				claims["aud"] = "other-client"
			},
		},
		{
			name: "issuer mismatch",
			mutateClaims: func(claims map[string]any) {
				claims["iss"] = "https://other-issuer.example"
			},
		},
		{
			name: "expired token",
			mutateClaims: func(claims map[string]any) {
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			},
		},
		{name: "invalid signature", wrongSignature: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			principal, _, err := runFakeOIDC(t, fakeOIDCOptions{
				mutateClaims:   tc.mutateClaims,
				wrongSignature: tc.wrongSignature,
			})
			if err == nil {
				t.Fatalf("Authenticate() returned principal %q for an invalid ID Token", principal.PrincipalID())
			}
			if principal.PrincipalID() != "" {
				t.Fatalf("Authenticate() returned a Principal on failure: %q", principal.PrincipalID())
			}
		})
	}
}

func TestAuthenticateRejectsStateMismatch(t *testing.T) {
	principal, _, err := runFakeOIDC(t, fakeOIDCOptions{wrongState: true})
	if err == nil {
		t.Fatal("Authenticate() succeeded with a mismatched state")
	}
	if principal.PrincipalID() != "" {
		t.Fatalf("Authenticate() returned a Principal on state mismatch: %q", principal.PrincipalID())
	}
}

func TestOIDCConfigRequiresTrustedIssuerAndLoopbackRedirect(t *testing.T) {
	base := OIDCConfig{
		Issuer:      "https://issuer.example",
		ClientID:    "taxion-cli",
		Audience:    "taxion-cli",
		RedirectURI: "http://127.0.0.1:8765/callback",
		Timeout:     time.Minute,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	cases := []OIDCConfig{
		{Issuer: "http://issuer.example", ClientID: base.ClientID, Audience: base.Audience, RedirectURI: base.RedirectURI},
		{Issuer: base.Issuer, ClientID: base.ClientID, Audience: base.Audience, RedirectURI: "http://example.com:8765/callback"},
		{Issuer: base.Issuer, ClientID: base.ClientID, Audience: base.Audience, RedirectURI: "http://127.0.0.1:8765/other"},
	}
	for _, config := range cases {
		if err := config.Validate(); err == nil {
			t.Fatalf("Validate() accepted invalid config %#v", config)
		}
	}
}

func TestValidateHTTPSURLRejectsDowngrade(t *testing.T) {
	if err := validateHTTPSURL("JWKS", "http://issuer.example/keys"); err == nil {
		t.Fatal("validateHTTPSURL() accepted an HTTP endpoint")
	}
	if err := validateHTTPSURL("token", "https://issuer.example/token"); err != nil {
		t.Fatalf("validateHTTPSURL() rejected HTTPS endpoint: %v", err)
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("expected-state", "expected-state") {
		t.Fatal("constantTimeEqual() rejected equal state")
	}
	if constantTimeEqual("expected-state", "different-state") {
		t.Fatal("constantTimeEqual() accepted different state")
	}
}

func TestOIDCHTTPClientRejectsDowngrade(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer downstream.Close()
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, downstream.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := newOIDCHTTPClient()
	client.Transport = redirector.Client().Transport
	if _, err := client.Get(redirector.URL); err == nil {
		t.Fatal("OIDC HTTP client followed an HTTPS-to-HTTP downgrade")
	}
}

func TestConfigFromEnvRequiresIssuerAndClientID(t *testing.T) {
	t.Setenv("TAXION_OIDC_ISSUER", "")
	t.Setenv("TAXION_OIDC_CLIENT_ID", "")
	t.Setenv("TAXION_OIDC_AUDIENCE", "")
	t.Setenv("TAXION_OIDC_REDIRECT_URI", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("ConfigFromEnv() succeeded without administrator OIDC configuration")
	}
}

func runFakeOIDC(t *testing.T, options fakeOIDCOptions) (AuthenticatedPrincipal, string, error) {
	t.Helper()
	trustedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate trusted signing key: %v", err)
	}
	signingKey := trustedKey
	if options.wrongSignature {
		signingKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate untrusted signing key: %v", err)
		}
	}

	provider := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{{
			PublicKey: trustedKey.Public(),
			KeyID:     "taxion-test-key",
			Algorithm: oidc.RS256,
		}},
	}
	var challenge, nonce, issuer string
	mux := http.NewServeMux()
	mux.Handle("/.well-known/openid-configuration", provider)
	mux.Handle("/keys", provider)
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		challenge = query.Get("code_challenge")
		nonce = query.Get("nonce")
		if query.Get("code_challenge_method") != "S256" || challenge == "" || nonce == "" || query.Get("state") == "" {
			http.Error(w, "PKCE, nonce, and state are required", http.StatusBadRequest)
			return
		}
		callback, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "invalid redirect URI", http.StatusBadRequest)
			return
		}
		state := query.Get("state")
		if options.wrongState {
			state = "wrong-state"
		}
		values := callback.Query()
		values.Set("code", "one-time-code")
		values.Set("state", state)
		callback.RawQuery = values.Encode()
		http.Redirect(w, r, callback.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid token request", http.StatusBadRequest)
			return
		}
		if r.Form.Get("code_verifier") == "" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge {
			http.Error(w, "PKCE verification failed", http.StatusBadRequest)
			return
		}
		claims := map[string]any{
			"iss":   issuer,
			"aud":   "taxion-cli",
			"sub":   "subject-123",
			"exp":   time.Now().Add(5 * time.Minute).Unix(),
			"iat":   time.Now().Unix(),
			"nonce": nonce,
		}
		if options.mutateClaims != nil {
			options.mutateClaims(claims)
		}
		rawClaims, err := json.Marshal(claims)
		if err != nil {
			http.Error(w, "encode claims", http.StatusInternalServerError)
			return
		}
		rawIDToken := oidctest.SignIDToken(signingKey, "taxion-test-key", oidc.RS256, string(rawClaims))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token-secret",
			"token_type":   "Bearer",
			"expires_in":   300,
			"id_token":     rawIDToken,
		})
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()
	provider.SetIssuer(server.URL)

	// The token endpoint needs the exact issuer configured by discovery.
	issuer = server.URL
	timeout := 5 * time.Second
	if options.wrongState {
		timeout = 2 * time.Second
	}
	config := OIDCConfig{
		Issuer:      server.URL,
		ClientID:    "taxion-cli",
		Audience:    "taxion-cli",
		RedirectURI: "http://127.0.0.1:0/callback",
		Timeout:     timeout,
	}
	authenticator := &OIDCAuthenticator{
		config:               config,
		httpClient:           server.Client(),
		openAuthorizationURL: func(authorizationURL string) error {
			response, err := server.Client().Get(authorizationURL)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, response.Body)
			return nil
		},
	}
	var output bytes.Buffer
	principal, authErr := authenticator.Authenticate(context.Background(), &output)
	return principal, output.String(), authErr
}
