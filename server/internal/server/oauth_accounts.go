package server

import (
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"
)

//go:embed oauth_select_account.html
var oauthSelectAccountHTML string

var oauthSelectAccountPage = template.Must(template.New("oauth-select-account").Parse(oauthSelectAccountHTML))
var oauthSelectAccountStyleHash = oauthPageStyleHash(oauthSelectAccountHTML)

const (
	// accountHintsCookieName remembers which GitHub identities completed a
	// callback in this browser so a denied sign-in can offer account
	// selection. It is encrypted, HttpOnly, scoped to /auth/ and grants no
	// authority.
	accountHintsCookieName = "cao_accounts"
	maxAccountHints        = 5
)

var errAccountNotAuthorized = errors.New("GitHub user is not authorized")

// githubLoginPattern accepts GitHub logins, including Enterprise Managed
// User logins with an underscore shortcode suffix.
var githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)

type accountHint struct {
	ID         int64  `json:"id"`
	Login      string `json:"login"`
	Authorized bool   `json:"authorized"`
	SeenAt     int64  `json:"seenAt"`
}

func (oauth *githubOAuth) accountHints(request *http.Request) []accountHint {
	cookie, err := request.Cookie(accountHintsCookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}
	plain, err := oauth.open(cookie.Value)
	if err != nil {
		return nil
	}
	var stored []accountHint
	if json.Unmarshal(plain, &stored) != nil {
		return nil
	}
	hints := make([]accountHint, 0, len(stored))
	for _, hint := range stored {
		if hint.ID <= 0 || !githubLoginPattern.MatchString(hint.Login) ||
			slices.ContainsFunc(hints, func(existing accountHint) bool { return existing.ID == hint.ID }) {
			continue
		}
		hints = append(hints, hint)
		if len(hints) == maxAccountHints {
			break
		}
	}
	return hints
}

// rememberAccount records the callback's GitHub identity as the most recent
// hint and returns the updated hints, most recent first.
func (oauth *githubOAuth) rememberAccount(response http.ResponseWriter, request *http.Request, account githubAccount, authorized bool) []accountHint {
	hints := oauth.accountHints(request)
	if account.ID <= 0 || !githubLoginPattern.MatchString(account.Login) {
		return hints
	}
	hints = slices.DeleteFunc(hints, func(hint accountHint) bool { return hint.ID == account.ID })
	hints = append([]accountHint{{ID: account.ID, Login: account.Login, Authorized: authorized, SeenAt: time.Now().Unix()}}, hints...)
	if len(hints) > maxAccountHints {
		hints = hints[:maxAccountHints]
	}
	data, err := json.Marshal(hints)
	if err != nil {
		return hints
	}
	sealed, err := oauth.seal(data)
	if err != nil {
		return hints
	}
	http.SetCookie(response, &http.Cookie{Name: accountHintsCookieName, Value: sealed, Path: "/auth/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
	return hints
}

func (oauth *githubOAuth) clearAccountHints(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{Name: accountHintsCookieName, Value: "", Path: "/auth/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func hasOtherAccount(hints []accountHint, id int64) bool {
	return slices.ContainsFunc(hints, func(hint accountHint) bool { return hint.ID != id })
}

// providerHome returns the configured GitHub host's web root, so account
// switching guidance points at github.com, a GHE.com subdomain or a GitHub
// Enterprise Server instance as configured.
func (oauth *githubOAuth) providerHome() (string, string) {
	target, err := url.Parse(oauth.config.AuthURL)
	if err != nil || target.Host == "" || (target.Scheme != "https" && target.Scheme != "http") {
		return "https://github.com/", "github.com"
	}
	return target.Scheme + "://" + target.Host + "/", target.Host
}

func (oauth *githubOAuth) selectAccount(response http.ResponseWriter, request *http.Request) {
	hints := oauth.accountHints(request)
	if len(hints) < 2 {
		oauth.logBranch(request.Context(), "select_account.single_identity")
		http.Redirect(response, request, "/auth/login?select_account=1", http.StatusFound)
		return
	}
	type accountChoice struct {
		Login      string
		Authorized bool
		LoginURL   string
	}
	home, host := oauth.providerHome()
	view := struct {
		Denied       string
		Host         string
		ProviderHome string
		Accounts     []accountChoice
	}{Host: host, ProviderHome: home}
	if !hints[0].Authorized {
		view.Denied = hints[0].Login
	}
	// Offer accounts that previously had access first.
	ordered := slices.Clone(hints)
	slices.SortStableFunc(ordered, func(a, b accountHint) int {
		switch {
		case a.Authorized == b.Authorized:
			return 0
		case a.Authorized:
			return -1
		default:
			return 1
		}
	})
	for _, hint := range ordered {
		view.Accounts = append(view.Accounts, accountChoice{
			Login: hint.Login, Authorized: hint.Authorized,
			LoginURL: "/auth/login?" + url.Values{"login": {hint.Login}}.Encode(),
		})
	}
	oauth.logBranch(request.Context(), "select_account.rendered")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'sha256-"+oauthSelectAccountStyleHash+"'; base-uri 'none'; form-action 'none'")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = oauthSelectAccountPage.Execute(response, view)
}
