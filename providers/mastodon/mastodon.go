// Package mastodon implements the OAuth2 protocol for authenticating users through Mastodon.
// This package can be used as a reference implementation of an OAuth2 provider for Goth.
package mastodon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/dental-teklif/goth"
	"golang.org/x/oauth2"
)

// Mastodon.social is the flagship instance of mastodon
var (
	InstanceURL = "https://mastodon.social/"
)

// Provider is the implementation of `goth.Provider` for accessing Mastodon.
type Provider struct {
	ClientKey    string
	Secret       string
	CallbackURL  string
	HTTPClient   *http.Client
	config       *oauth2.Config
	providerName string
	authURL      string
	tokenURL     string
	profileURL   string
}

// New creates a new Mastodon provider and sets up important connection details.
// You should always call `mastodon.New` to get a new provider.  Never try to
// create one manually.
func New(clientKey, secret, callbackURL string, scopes ...string) *Provider {
	return NewCustomisedURL(clientKey, secret, callbackURL, InstanceURL, scopes...)
}

// NewCustomisedURL is similar to New(...) but can be used to set custom URLs to connect to
func NewCustomisedURL(clientKey, secret, callbackURL, instanceURL string, scopes ...string) *Provider {
	instanceURL = fmt.Sprintf("%s/", strings.TrimSuffix(instanceURL, "/"))
	profileURL := fmt.Sprintf("%sapi/v1/accounts/verify_credentials", instanceURL)
	authURL := fmt.Sprintf("%soauth/authorize", instanceURL)
	tokenURL := fmt.Sprintf("%soauth/token", instanceURL)
	p := &Provider{
		ClientKey:    clientKey,
		Secret:       secret,
		CallbackURL:  callbackURL,
		providerName: "mastodon",
		profileURL:   profileURL,
	}
	p.config = newConfig(p, authURL, tokenURL, scopes)
	return p
}

// Name is the name used to retrieve this provider later.
func (p *Provider) Name() string {
	return p.providerName
}

// SetName is to update the name of the provider (needed in case of multiple providers of 1 type)
func (p *Provider) SetName(name string) {
	p.providerName = name
}

func (p *Provider) Client() *http.Client {
	return goth.HTTPClientWithFallBack(p.HTTPClient)
}

// Debug is a no-op for the Mastodon package.
func (p *Provider) Debug(debug bool) {}

// BeginAuth asks Mastodon for an authentication end-point.
func (p *Provider) BeginAuth(state string) (goth.Session, error) {
	return &Session{
		AuthURL: p.config.AuthCodeURL(state),
	}, nil
}

// FetchUser will go to Mastodon and access basic information about the user.
func (p *Provider) FetchUser(session goth.Session) (goth.User, error) {
	sess := session.(*Session)
	user := goth.User{
		AccessToken:  sess.AccessToken,
		Provider:     p.Name(),
		RefreshToken: sess.RefreshToken,
		ExpiresAt:    sess.ExpiresAt,
	}

	if user.AccessToken == "" {
		// data is not yet retrieved since accessToken is still empty
		return user, fmt.Errorf("%s cannot get user information without accessToken", p.providerName)
	}

	req, err := http.NewRequest("GET", p.profileURL, nil)
	if err != nil {
		return user, err
	}

	req.Header.Add("Authorization", "Bearer "+sess.AccessToken)
	response, err := p.Client().Do(req)
	if err != nil {
		return user, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return user, fmt.Errorf("%s responded with a %d trying to fetch user information", p.providerName, response.StatusCode)
	}

	bits, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return user, err
	}

	err = json.NewDecoder(bytes.NewReader(bits)).Decode(&user.RawData)
	if err != nil {
		return user, err
	}

	err = userFromReader(bytes.NewReader(bits), &user)

	return user, err
}

func newConfig(provider *Provider, authURL, tokenURL string, scopes []string) *oauth2.Config {
	c := &oauth2.Config{
		ClientID:     provider.ClientKey,
		ClientSecret: provider.Secret,
		RedirectURL:  provider.CallbackURL,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
		Scopes: []string{},
	}

	if len(scopes) > 0 {
		for _, scope := range scopes {
			c.Scopes = append(c.Scopes, scope)
		}
	}
	return c
}

func userFromReader(r io.Reader, user *goth.User) error {
	u := struct {
		Name      string `json:"display_name"`
		NickName  string `json:"username"`
		ID        string `json:"id"`
		AvatarURL string `json:"avatar"`
	}{}
	err := json.NewDecoder(r).Decode(&u)
	if err != nil {
		return err
	}
	user.Name = u.Name
	if len(user.Name) == 0 {
		user.Name = u.NickName
	}
	user.NickName = u.NickName
	user.UserID = u.ID
	user.AvatarURL = u.AvatarURL
	return nil
}

// RefreshTokenAvailable refresh token is provided by auth provider or not
func (p *Provider) RefreshTokenAvailable() bool {
	return true
}

// RefreshToken get new access token based on the refresh token
func (p *Provider) RefreshToken(refreshToken string) (*oauth2.Token, error) {
	token := &oauth2.Token{RefreshToken: refreshToken}
	ts := p.config.TokenSource(goth.ContextForClient(p.Client()), token)
	newToken, err := ts.Token()
	if err != nil {
		return nil, err
	}
	return newToken, err
}
