package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/hiono/taxion/internal/identity"
	"golang.org/x/oauth2"
)

const (
	defaultRedirectURI = "http://127.0.0.1:8765/callback"
	callbackPath       = "/callback"
)

type OIDCConfig struct {
	Issuer      string
	ClientID    string
	Audience    string
	RedirectURI string
	Timeout     time.Duration
}

type OIDCAuthenticator struct {
	config               OIDCConfig
	httpClient           *http.Client
	openAuthorizationURL func(string) error
	listen               func(string, string) (net.Listener, error)
}

type callbackResult struct {
	code string
	err  error
}

func ConfigFromEnv() (OIDCConfig, error) {
	config := OIDCConfig{
		Issuer:      strings.TrimSpace(os.Getenv("TAXION_OIDC_ISSUER")),
		ClientID:    strings.TrimSpace(os.Getenv("TAXION_OIDC_CLIENT_ID")),
		Audience:    strings.TrimSpace(os.Getenv("TAXION_OIDC_AUDIENCE")),
		RedirectURI: strings.TrimSpace(os.Getenv("TAXION_OIDC_REDIRECT_URI")),
		Timeout:     5 * time.Minute,
	}
	if config.Issuer == "" {
		return OIDCConfig{}, errors.New("TAXION_OIDC_ISSUER is required")
	}
	if config.ClientID == "" {
		return OIDCConfig{}, errors.New("TAXION_OIDC_CLIENT_ID is required")
	}
	if config.Audience == "" {
		config.Audience = config.ClientID
	}
	if config.RedirectURI == "" {
		config.RedirectURI = defaultRedirectURI
	}
	return config, config.Validate()
}

func NewOIDCAuthenticator(config OIDCConfig) (*OIDCAuthenticator, error) {
	if config.Audience == "" {
		config.Audience = config.ClientID
	}
	if config.RedirectURI == "" {
		config.RedirectURI = defaultRedirectURI
	}
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Minute
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &OIDCAuthenticator{config: config, httpClient: newOIDCHTTPClient(), listen: net.Listen}, nil
}

func (c OIDCConfig) Validate() error {
	issuer, err := url.Parse(c.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("TAXION_OIDC_ISSUER must be an absolute HTTPS URL")
	}
	if strings.TrimSpace(c.ClientID) == "" {
		return errors.New("TAXION_OIDC_CLIENT_ID is required")
	}
	if strings.TrimSpace(c.Audience) == "" {
		return errors.New("TAXION_OIDC_AUDIENCE is required")
	}
	redirect, err := url.Parse(c.RedirectURI)
	if err != nil || redirect.Scheme != "http" || redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" || redirect.Path != callbackPath {
		return errors.New("TAXION_OIDC_REDIRECT_URI must be an HTTP loopback URL ending in /callback")
	}
	ip := net.ParseIP(redirect.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("TAXION_OIDC_REDIRECT_URI must use a loopback IP address")
	}
	port, err := strconv.Atoi(redirect.Port())
	if err != nil || port < 0 || port > 65535 {
		return errors.New("TAXION_OIDC_REDIRECT_URI must include a valid loopback port")
	}
	if c.Timeout < 0 {
		return errors.New("OIDC timeout must not be negative")
	}
	return nil
}

func (a *OIDCAuthenticator) Authenticate(ctx context.Context, output io.Writer) (identity.AuthenticatedPrincipal, error) {
	if output == nil {
		output = io.Discard
	}
	ctx, cancel := context.WithTimeout(ctx, a.config.Timeout)
	defer cancel()

	providerContext := ctx
	if a.httpClient != nil {
		providerContext = oidc.ClientContext(ctx, a.httpClient)
	}
	provider, err := oidc.NewProvider(providerContext, a.config.Issuer)
	if err != nil {
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("discover configured OIDC issuer: %w", err)
	}
	if err := validateProviderEndpoints(provider); err != nil {
		return identity.AuthenticatedPrincipal{}, err
	}

	redirect, _ := url.Parse(a.config.RedirectURI)
	listen := a.listen
	if listen == nil {
		listen = net.Listen
	}
	listener, err := listen("tcp", net.JoinHostPort(redirect.Hostname(), redirect.Port()))
	if err != nil {
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("listen on OIDC loopback callback: %w", err)
	}
	defer listener.Close()
	redirect.Host = listener.Addr().String()
	redirectURI := redirect.String()

	state, err := randomURLToken(32)
	if err != nil {
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("generate OIDC state: %w", err)
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("generate OIDC nonce: %w", err)
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("generate PKCE verifier: %w", err)
	}

	oauthConfig := oauth2.Config{
		ClientID:    a.config.ClientID,
		RedirectURL: redirectURI,
		Endpoint:    provider.Endpoint(),
		Scopes:      []string{oidc.ScopeOpenID},
	}
	authorizationURL := oauthConfig.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
		oidc.Nonce(nonce),
	)

	callbacks := make(chan callbackResult, 1)
	var accepted atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != callbackPath {
			http.NotFound(w, r)
			return
		}
		if !constantTimeEqual(r.URL.Query().Get("state"), state) {
			http.Error(w, "Invalid authentication state.", http.StatusBadRequest)
			return
		}
		if !accepted.CompareAndSwap(false, true) {
			http.Error(w, "Authentication callback already used.", http.StatusConflict)
			return
		}
		if r.URL.Query().Get("error") != "" {
			callbacks <- callbackResult{err: errors.New("OIDC authorization was rejected")}
			http.Error(w, "Authentication was not completed.", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			callbacks <- callbackResult{err: errors.New("OIDC callback did not contain an authorization code")}
			http.Error(w, "Authentication was not completed.", http.StatusBadRequest)
			return
		}
		callbacks <- callbackResult{code: code}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "Authentication completed. Return to Taxion CLI.")
	})

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
		}
	}()
	defer func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownContext)
	}()

	if a.openAuthorizationURL == nil {
		if _, err := fmt.Fprintf(output, "Open this URL to authenticate with the configured identity provider:\n%s\n", authorizationURL); err != nil {
			return identity.AuthenticatedPrincipal{}, fmt.Errorf("write OIDC authorization URL: %w", err)
		}
	} else if err := a.openAuthorizationURL(authorizationURL); err != nil {
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("open OIDC authorization URL: %w", err)
	}

	var callback callbackResult
	select {
	case callback = <-callbacks:
	case err := <-serveErrors:
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("serve OIDC callback: %w", err)
	case <-ctx.Done():
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("wait for OIDC callback: %w", ctx.Err())
	}
	if callback.err != nil {
		return identity.AuthenticatedPrincipal{}, callback.err
	}

	token, err := oauthConfig.Exchange(providerContext, callback.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("exchange OIDC authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return identity.AuthenticatedPrincipal{}, errors.New("OIDC token response did not contain an ID Token")
	}

	idToken, err := provider.Verifier(&oidc.Config{ClientID: a.config.ClientID}).Verify(providerContext, rawIDToken)
	if err != nil {
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("verify OIDC ID Token: %w", err)
	}
	if !constantTimeEqual(idToken.Nonce, nonce) {
		return identity.AuthenticatedPrincipal{}, errors.New("OIDC ID Token nonce mismatch")
	}
	if idToken.Issuer != a.config.Issuer {
		return identity.AuthenticatedPrincipal{}, errors.New("OIDC ID Token issuer mismatch")
	}
	if !containsAudience(idToken.Audience, a.config.Audience) {
		return identity.AuthenticatedPrincipal{}, errors.New("OIDC ID Token audience mismatch")
	}
	if strings.TrimSpace(idToken.Subject) == "" {
		return identity.AuthenticatedPrincipal{}, errors.New("OIDC ID Token subject is required")
	}

	principal, err := identity.NewAuthenticatedPrincipal(idToken.Issuer, idToken.Subject, time.Now().UTC())
	if err != nil {
		return identity.AuthenticatedPrincipal{}, fmt.Errorf("create authenticated principal: %w", err)
	}
	return principal, nil
}

func randomURLToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func containsAudience(audiences []string, expected string) bool {
	for _, audience := range audiences {
		if audience == expected {
			return true
		}
	}
	return false
}

func validateProviderEndpoints(provider *oidc.Provider) error {
	endpoint := provider.Endpoint()
	for _, item := range []struct {
		name string
		url  string
	}{
		{name: "authorization", url: endpoint.AuthURL},
		{name: "token", url: endpoint.TokenURL},
	} {
		if err := validateHTTPSURL(item.name, item.url); err != nil {
			return err
		}
	}
	var metadata struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return fmt.Errorf("read OIDC provider metadata: %w", err)
	}
	return validateHTTPSURL("JWKS", metadata.JWKSURI)
}

func validateHTTPSURL(name, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("OIDC %s endpoint must be an absolute HTTPS URL", name)
	}
	return nil
}

func newOIDCHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("OIDC endpoint redirect must remain HTTPS")
			}
			if len(via) >= 10 {
				return errors.New("too many OIDC endpoint redirects")
			}
			return nil
		},
	}
}
