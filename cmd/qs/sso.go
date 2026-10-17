package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// ssoTimeout is how long qs waits for the browser sign-in.
const ssoTimeout = 5 * time.Minute

// authMethods is how the server lets users sign in.
type authMethods struct {
	Password bool `json:"password"`
	SSO      *struct {
		Name string `json:"name"`
	} `json:"sso"`
}

// authMethods asks the server how to sign in; servers from before single
// sign-on only have passwords.
func (c *client) authMethods() (authMethods, error) {
	var m authMethods
	err := c.do("GET", "/api/auth/methods", nil, &m)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return authMethods{Password: true}, nil
	}
	return m, err
}

// ssoLogin signs in with single sign-on in a browser and returns a new API
// token and the username. The server hands the finished sign-in to a
// listener on 127.0.0.1 as a one-time code, which only works together with
// a verifier that never leaves qs.
func (c *client) ssoLogin(name string) (token, username string, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	defer ln.Close()
	verifier := make([]byte, 32)
	rand.Read(verifier)
	v := base64.RawURLEncoding.EncodeToString(verifier)
	sum := sha256.Sum256([]byte(v))
	loginURL := c.Server + "/api/auth/oidc/login?" + url.Values{
		"cli_port":      {fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)},
		"cli_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
	}.Encode()

	type result struct{ code, err string }
	results := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		res := result{code: q.Get("code"), err: q.Get("error")}
		msg := "Signed in to Quicksand. You can close this tab and return to the terminal."
		if res.code == "" {
			if res.err == "" {
				res.err = "no sign-in code"
			}
			msg = "Sign-in failed: " + res.err
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>Quicksand</title><p style=\"font-family:sans-serif\">%s</p>", html.EscapeString(msg))
		select {
		case results <- res:
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Fprintf(os.Stderr, "Sign in with %s in your browser. If it doesn't open, visit:\n  %s\n", name, loginURL)
	openBrowser(loginURL)
	var res result
	select {
	case res = <-results:
	case <-time.After(ssoTimeout):
		return "", "", errors.New("timed out waiting for the browser sign-in")
	}
	if res.code == "" {
		return "", "", fmt.Errorf("sign in failed: %s", res.err)
	}
	var out struct {
		Token string `json:"token"`
		User  struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	err = c.do("POST", "/api/auth/cli/token", map[string]string{"code": res.code, "verifier": v, "name": tokenName()}, &out)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
		return "", "", errors.New("sign in failed: the server refused the sign-in code; try again")
	} else if err != nil {
		return "", "", fmt.Errorf("sign in failed: %w", err)
	}
	if out.Token == "" {
		return "", "", errors.New("the server didn't return a token")
	}
	return out.Token, out.User.Username, nil
}

// openBrowser opens url in the default browser, if it can.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}
