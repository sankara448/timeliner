package oauth2client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

// RemoteAppSource implements oauth2.TokenSource for
// OAuth2 client apps that have their credentials
// (Client ID and Secret, as well as endpoint info)
// stored remotely. Thus, this type obtains tokens
// through a remote proxy that presumably has the
// client app credentials, which it will replace
// before proxying to the provider.
//
// RemoteAppSource values can be ephemeral.
type RemoteAppSource struct {
	// How to obtain the auth URL.
	// Default: DirectAuthURLMode
	AuthURLMode AuthURLMode

	// The URL to the proxy server (its
	// address + base path).
	ProxyURL string

	// The ID of the OAuth2 provider.
	ProviderID string

	// The scopes for which to obtain
	// authorization.
	Scopes []string

	// The URL to redirect to to finish
	// the ceremony.
	RedirectURL string

	// How the auth code is obtained.
	// If not set, a default
	// oauth2code.Browser is used.
	AuthCodeGetter Getter
}

// Config returns an OAuth2 config.
func (s RemoteAppSource) Config() *oauth2.Config {
	redirURL := s.RedirectURL
	if redirURL == "" {
		redirURL = DefaultRedirectURL
	}

	return &oauth2.Config{
		ClientID:     "placeholder",
		ClientSecret: "placeholder",
		RedirectURL:  redirURL,
		Scopes:       s.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  s.ProxyURL + "/proxy/" + s.ProviderID + "/auth",
			TokenURL: s.ProxyURL + "/proxy/" + s.ProviderID + "/token",
		},
	}
}

// Token obtains a token.
func (s RemoteAppSource) Token() (*oauth2.Token, error) {
	if s.AuthCodeGetter == nil {
		s.AuthCodeGetter = Browser{}
	}
	if s.AuthURLMode == "" {
		s.AuthURLMode = DirectAuthURLMode
	}

	cfg := s.Config()

	// obtain a state value and auth URL
	var stateVal, authURL string
	var err error
	switch s.AuthURLMode {
	case DirectAuthURLMode:
		stateVal, authURL, err = s.getDirectAuthURLFromProxy()
	case ProxiedAuthURLMode:
		stateVal, authURL, err = s.getProxiedAuthURL(cfg)
	default:
		return nil, fmt.Errorf("unknown AuthURLMode: %s", s.AuthURLMode)
	}
	if err != nil {
		return nil, err
	}

	// now obtain the code
	code, err := s.AuthCodeGetter.Get(stateVal, authURL)
	if err != nil {
		return nil, fmt.Errorf("getting code via browser: %v", err)
	}

	// and complete the ceremony
	ctx := context.WithValue(context.Background(),
		oauth2.HTTPClient, httpClient)

	return cfg.Exchange(ctx, code)
}

// getDirectAuthURLFromProxy returns an auth URL that goes directly to the
// OAuth2 provider server, but it gets that URL by querying the proxy server
// for what it should be ("DirectAuthURLMode").
func (s RemoteAppSource) getDirectAuthURLFromProxy() (state string, authURL string, err error) {
	redirURL := s.RedirectURL
	if redirURL == "" {
		redirURL = DefaultRedirectURL
	}

	v := url.Values{
		"provider": {s.ProviderID},
		"scope":    s.Scopes,
		"redirect": {redirURL},
	}

	proxyURL := strings.TrimSuffix(s.ProxyURL, "/")
	resp, err := http.Get(proxyURL + "/auth-code-url?" + v.Encode())
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("requesting auth code URL from proxy: HTTP %d: %s",
			resp.StatusCode, resp.Status)
	}

	var info OAuth2Info
	err = json.NewDecoder(resp.Body).Decode(&info)
	if err != nil {
		return "", "", err
	}

	return info.StateValue, info.AuthCodeURL, nil
}

// getProxiedAuthURL returns an auth URL that goes to the remote proxy ("ProxiedAuthURLMode").
func (s RemoteAppSource) getProxiedAuthURL(cfg *oauth2.Config) (state string, authURL string, err error) {
	state = State()
	authURL = cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
	return
}

// AuthURLMode describes what kind of auth URL a
// RemoteAppSource should obtain.
type AuthURLMode string

const (
	// DirectAuthURLMode queries the remote proxy to get
	// an auth URL that goes directly to the OAuth2 provider
	// web page the user must go to in order to obtain
	// authorization. Although this mode incurs one extra
	// HTTP request (that is not part of the OAuth2 spec,
	// it is purely our own), it is perhaps more robust in
	// more environments, since the browser will access the
	// auth provider's site directly, meaning that any HTML
	// or JavaScript on the page that expects HTTPS or a
	// certain hostname will be able to function correctly.
	DirectAuthURLMode AuthURLMode = "direct"

	// ProxiedAuthURLMode makes an auth URL that goes to
	// the remote proxy, not directly to the provider.
	// This is perhaps a "purer" approach than
	// DirectAuthURLMode, but it may not work if HTML or
	// JavaScript on the provider's auth page expects
	// a certain scheme or hostname in the page's URL.
	// This mode usually works when the proxy is running
	// over HTTPS, but this mode may break depending on
	// the provider, when the proxy uses HTTP (which
	// should only be in dev environments of course).
	//
	// For example, Google's OAuth2 page will try to set a
	// secure-context cookie using JavaScript, which fails
	// if the auth page is proxied through a plaintext HTTP
	// localhost endpoint, which is what we do during
	// development for convenience; the lack of HTTPS caused
	// the page to reload infinitely because, even though
	// the request was reverse-proxied, the JS on the page
	// expected HTTPS. (See my self-congratulatory tweet:
	// https://twitter.com/mholt6/status/1078518306045231104)
	// Using DirectAuthURLMode is the easiest way around
	// this problem.
	ProxiedAuthURLMode AuthURLMode = "proxied"
)

var _ App = RemoteAppSource{}
