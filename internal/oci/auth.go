package oci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"airgapkit/internal/config"
	"airgapkit/internal/dl"
)

// authenticator performs the registry token dance and caches one token per
// repository scope.
type authenticator struct {
	client *dl.Client
	auths  map[string]config.Auth

	mu     sync.Mutex
	tokens map[string]string
}

func newAuthenticator(c *dl.Client, auths map[string]config.Auth) *authenticator {
	return &authenticator{client: c, auths: auths, tokens: map[string]string{}}
}

// Header returns the Authorization header to use for a repository.
func (a *authenticator) Header(ctx context.Context, ref Ref, actions string) (http.Header, error) {
	scope := fmt.Sprintf("repository:%s:%s", ref.Repository, actions)
	key := ref.Host() + "|" + scope
	a.mu.Lock()
	tok, ok := a.tokens[key]
	a.mu.Unlock()
	if ok {
		h := http.Header{}
		h.Set("Authorization", tok)
		return h, nil
	}

	challenge, err := a.challenge(ctx, ref)
	if err != nil {
		return nil, err
	}
	cred := a.credential(ref.Registry)

	var value string
	switch {
	case challenge == nil:
		// No authentication required at all.
		if cred.Username != "" {
			value = basic(cred)
		}
	case strings.EqualFold(challenge.scheme, "bearer"):
		value, err = a.bearer(ctx, challenge, scope, cred)
		if err != nil {
			return nil, err
		}
	case strings.EqualFold(challenge.scheme, "basic"):
		value = basic(cred)
	}

	a.mu.Lock()
	a.tokens[key] = value
	a.mu.Unlock()
	h := http.Header{}
	if value != "" {
		h.Set("Authorization", value)
	}
	return h, nil
}

type authChallenge struct {
	scheme  string
	realm   string
	service string
}

// challenge probes /v2/ and parses the WWW-Authenticate header.
func (a *authenticator) challenge(ctx context.Context, ref Ref) (*authChallenge, error) {
	resp, err := a.client.Request(ctx, http.MethodGet, ref.BaseURL()+"/", nil)
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", ref.BaseURL()+"/", err)
	}
	defer func() {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusUnauthorized {
		return nil, nil
	}
	hdr := resp.Header.Get("WWW-Authenticate")
	if hdr == "" {
		return nil, nil
	}
	parts := strings.SplitN(hdr, " ", 2)
	ch := &authChallenge{scheme: parts[0]}
	if len(parts) == 2 {
		for _, kv := range splitParams(parts[1]) {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"`)
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "realm":
				ch.realm = v
			case "service":
				ch.service = v
			}
		}
	}
	return ch, nil
}

// splitParams splits a WWW-Authenticate parameter list on commas that are not
// inside quotes.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ',' && !inQuote:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func (a *authenticator) bearer(ctx context.Context, ch *authChallenge, scope string, cred config.Auth) (string, error) {
	if ch.realm == "" {
		return "", fmt.Errorf("bearer challenge without realm")
	}
	u, err := url.Parse(ch.realm)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if ch.service != "" {
		q.Set("service", ch.service)
	}
	q.Set("scope", scope)
	u.RawQuery = q.Encode()

	h := http.Header{}
	if cred.Username != "" || cred.Password != "" {
		h.Set("Authorization", basic(cred))
	}
	body, _, err := a.client.GetBytes(ctx, u.String(), h)
	if err != nil {
		return "", fmt.Errorf("token request %s: %w", u.Redacted(), err)
	}
	var tr struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("token response: %w", err)
	}
	tok := tr.Token
	if tok == "" {
		tok = tr.AccessToken
	}
	if tok == "" {
		return "", fmt.Errorf("token response contained no token")
	}
	return "Bearer " + tok, nil
}

func basic(c config.Auth) string {
	if c.Token != "" {
		return "Bearer " + c.Token
	}
	if c.Username == "" && c.Password == "" {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))
}

func (a *authenticator) credential(registry string) config.Auth {
	if c, ok := a.auths[registry]; ok {
		return c
	}
	if registry == "docker.io" {
		if c, ok := a.auths["registry-1.docker.io"]; ok {
			return c
		}
		if c, ok := a.auths["https://index.docker.io/v1/"]; ok {
			return c
		}
	}
	return config.Auth{}
}
