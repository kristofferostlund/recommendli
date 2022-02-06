package recommendations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	uuid "github.com/satori/go.uuid"
	"github.com/zmb3/spotify"
	"golang.org/x/oauth2"

	"github.com/kristofferostlund/recommendli/pkg/logging"
	"github.com/kristofferostlund/recommendli/pkg/srv"
)

type ctxTokenType string

const (
	ctxTokenKey ctxTokenType = "SpotifyClient"

	CookieState        = "recommendli_authstate"
	CookieGoto         = "recommendli_goto"
	CookieSpotifyToken = "recommendli_spotifytoken"
)

var NoAuthenticationError error = errors.New("No authentication found")

type AuthAdaptor struct {
	authenticator              spotify.Authenticator
	redirectURL, uiRedirectURL url.URL

	log logging.Logger
}

func NewSpotifyAuthAdaptor(clientID, clientSecret string, redirectURL, uiRedirectURL url.URL, log logging.Logger) *AuthAdaptor {
	authenticator := spotify.NewAuthenticator(
		redirectURL.String(),
		spotify.ScopeUserReadPrivate,
		spotify.ScopePlaylistReadPrivate,
		spotify.ScopePlaylistModifyPrivate,
		spotify.ScopePlaylistModifyPublic,
		spotify.ScopeUserTopRead,
		spotify.ScopeUserReadCurrentlyPlaying,
		spotify.ScopeUserReadPlaybackState,
	)
	authenticator.SetAuthInfo(clientID, clientSecret)

	return &AuthAdaptor{
		authenticator: authenticator,
		redirectURL:   redirectURL,
		uiRedirectURL: uiRedirectURL,
		log:           log,
	}
}

func (a *AuthAdaptor) Path() string {
	return a.redirectURL.Path
}

func (a *AuthAdaptor) UIRedirectPath() string {
	return a.uiRedirectURL.Path
}

func (a *AuthAdaptor) TokenCallbackHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie(CookieState)
		if c == nil || c.Value == "" {
			a.log.Error("Missing required cookie", fmt.Errorf("Missing required cookie %s", CookieState))
			srv.InternalServerError(w)
			return
		}

		state, err := url.QueryUnescape(c.Value)
		srv.ClearCookie(w, c)
		if err != nil {
			a.log.Error("Failed to escape state", err)
			srv.InternalServerError(w)
			return
		}

		token, err := a.authenticator.Token(state, r)
		if err != nil {
			a.log.Error("Failed to get token", err)
			srv.InternalServerError(w)
			return
		}

		tokenB, err := json.Marshal(token)
		if err != nil {
			a.log.Error("Failed to marshal token", err)
			srv.InternalServerError(w)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     CookieSpotifyToken,
			Value:    base64.StdEncoding.EncodeToString(tokenB),
			Expires:  token.Expiry,
			Path:     "/",
			Secure:   true,
			HttpOnly: true,
			// @TODO: Read up on what cookie method to use so this is actually secure
			SameSite: http.SameSiteLaxMode,
		})

		gc, _ := r.Cookie(CookieGoto)
		if gc != nil && gc.Value != "" {
			redirectTo, err := url.QueryUnescape(gc.Value)
			srv.ClearCookie(w, gc)
			if err != nil {
				a.log.Error("Failed to get token", err)
				srv.InternalServerError(w)
				return
			}

			http.Redirect(w, r, redirectTo, http.StatusTemporaryRedirect)
			return
		}

		w.Write([]byte("OK"))
	}
}

func (a *AuthAdaptor) UIRedirectHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectTo := r.URL.Query().Get("url")
		if redirectTo == "" {
			a.log.Warn("No url provided, cannot redirect client")
			srv.JSONError(w, errors.New("url is a required paramter"), srv.Status(400))
			return
		}
		a.redirect(w, r, redirectTo)
	})
}

func (a *AuthAdaptor) Middleware() func(h http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, _ := r.Cookie(CookieSpotifyToken)
			if c != nil && c.Value != "" {
				token := &oauth2.Token{}
				decoded, _ := base64.StdEncoding.DecodeString(c.Value)
				err := json.Unmarshal(decoded, token)
				if err != nil {
					a.log.Warn("Failed to unmarshal token", "error", err)
					a.redirect(w, r, r.URL.String())
					return
				}
				if !token.Valid() {
					a.redirect(w, r, r.URL.String())
					return
				}

				r = r.WithContext(context.WithValue(r.Context(), ctxTokenKey, token))
				h.ServeHTTP(w, r)
				return
			}

			a.redirect(w, r, r.URL.String())
		})
	}
}

func (a *AuthAdaptor) GetClient(r *http.Request) (spotify.Client, error) {
	token, ok := r.Context().Value(ctxTokenKey).(*oauth2.Token)
	if !ok {
		return spotify.Client{}, NoAuthenticationError
	}
	client := a.authenticator.NewClient(token)
	client.AutoRetry = true
	return client, nil
}

func (a *AuthAdaptor) redirect(w http.ResponseWriter, r *http.Request, redirectBackTo string) {
	state := uuid.NewV4().String()

	http.SetCookie(w, &http.Cookie{
		Name:     CookieState,
		Value:    url.QueryEscape(state),
		Expires:  time.Now().Add(time.Hour),
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		// @TODO: Read up on what cookie method to use so this is actually secure
		SameSite: http.SameSiteLaxMode,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     CookieGoto,
		Value:    url.QueryEscape(redirectBackTo),
		Expires:  time.Now().Add(time.Hour),
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		// @TODO: Read up on what cookie method to use so this is actually secure
		SameSite: http.SameSiteLaxMode,
	})

	authURL := a.authenticator.AuthURL(state)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}
