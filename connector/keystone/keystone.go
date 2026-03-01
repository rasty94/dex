// Package keystone provides authentication strategy using Keystone.
package keystone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dexidp/dex/connector"
)

var (
	_ connector.PasswordConnector = (*conn)(nil)
	_ connector.RefreshConnector  = (*conn)(nil)
)

type conn struct {
	Domain        domainKeystone
	Host          string
	AdminUsername string
	AdminPassword string
	client        *http.Client
	Logger        *slog.Logger
	UserIDKey     string
	tokenCache    *timeCache
	groupMap      map[string]string
	fetchRoles    bool
}

type contextKey string

var (
	DomainContextKey  = contextKey("domain")
	TOTPContextKey    = contextKey("totp")
	ReceiptContextKey = contextKey("receipt")
)

type ErrTOTPRequired struct {
	Receipt string
}

func (e ErrTOTPRequired) Error() string {
	return "keystone: totp required"
}

type userKeystone struct {
	Domain domainKeystone `json:"domain"`
	ID     string         `json:"id"`
	Name   string         `json:"name"`
}

type domainKeystone struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Config holds the configuration parameters for Keystone connector.
// Keystone should expose API v3
// An example config:
//
//	connectors:
//		type: keystone
//		id: keystone
//		name: Keystone
//		config:
//			keystoneHost: http://example:5000
//			domain: default
//			keystoneUsername: demo
//			keystonePassword: DEMO_PASS
//			cacheTTL: "5m"
//			fetchRoles: true
//			groupMapping:
//			  "admin@my-project": "platform-admins"
type Config struct {
	Domain        string            `json:"domain"`
	Host          string            `json:"keystoneHost"`
	AdminUsername string            `json:"keystoneUsername"`
	AdminPassword string            `json:"keystonePassword"`
	UserIDKey     string            `json:"userIDKey"`
	CacheTTL      string            `json:"cacheTTL"`
	FetchRoles    bool              `json:"fetchRoles"`
	GroupMapping  map[string]string `json:"groupMapping"`
}

type loginRequestData struct {
	auth `json:"auth"`
}

type auth struct {
	Identity identity `json:"identity"`
}

type identity struct {
	Methods               []string               `json:"methods"`
	Password              *password              `json:"password,omitempty"`
	ApplicationCredential *applicationCredential `json:"application_credential,omitempty"`
	TOTP                  *totp                  `json:"totp,omitempty"`
}

type applicationCredential struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

// totp method structure
type totp struct {
	User userTOTP `json:"user"`
}

type userTOTP struct {
	Name     string         `json:"name"`
	Domain   domainKeystone `json:"domain"`
	Passcode string         `json:"passcode"`
}

type password struct {
	User user `json:"user"`
}

type user struct {
	Name     string         `json:"name"`
	Domain   domainKeystone `json:"domain"`
	Password string         `json:"password"`
}

type token struct {
	User userKeystone `json:"user"`
}

type tokenResponse struct {
	Token token `json:"token"`
}

type group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type groupsResponse struct {
	Groups []group `json:"groups"`
}

type userResponse struct {
	User struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		ID    string `json:"id"`
	} `json:"user"`
}

// Open returns an authentication strategy using Keystone.
func (c *Config) Open(id string, logger *slog.Logger) (connector.Connector, error) {
	_, err := uuid.Parse(c.Domain)
	var domain domainKeystone
	// check if the supplied domain is a UUID or the special "default" value
	// which is treated as an ID and not a name
	if err == nil || c.Domain == "default" {
		domain = domainKeystone{
			ID: c.Domain,
		}
	} else {
		domain = domainKeystone{
			Name: c.Domain,
		}
	}

	var tokenCache *timeCache
	if c.CacheTTL != "" {
		importTime, err := time.ParseDuration(c.CacheTTL)
		if err == nil && importTime > 0 {
			tokenCache = newTimeCache(importTime)
		}
	}

	return &conn{
		Domain:        domain,
		Host:          c.Host,
		AdminUsername: c.AdminUsername,
		AdminPassword: c.AdminPassword,
		Logger:        logger.With(slog.Group("connector", "type", "keystone", "id", id)),
		client:        http.DefaultClient,
		UserIDKey:     c.UserIDKey,
		tokenCache:    tokenCache,
		groupMap:      c.GroupMapping,
		fetchRoles:    c.FetchRoles,
	}, nil
}

func (p *conn) Close() error { return nil }

func (p *conn) Login(ctx context.Context, scopes connector.Scopes, username, password string) (identity connector.Identity, validPassword bool, err error) {
	// determine domain to use for this login: either an override from context
	// or the connector-configured domain.
	domain := p.Domain
	if v := ctx.Value(DomainContextKey); v != nil {
		if ds, ok := v.(string); ok && ds != "" {
			if _, err := uuid.Parse(ds); err == nil || ds == "default" {
				domain = domainKeystone{ID: ds}
			} else {
				domain = domainKeystone{Name: ds}
			}
		}
	}

	resp, err := p.getTokenResponse(ctx, username, password, domain)
	if err != nil {
		return identity, false, fmt.Errorf("keystone: error %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		bodyBytes, _ := io.ReadAll(resp.Body)

		if resp.StatusCode == 401 {
			receipt := resp.Header.Get("openstack-auth-receipt")
			if receipt != "" {
				return identity, false, ErrTOTPRequired{Receipt: receipt}
			}
			// If it's 401 without receipt, the password or TOTP is invalid
			return identity, false, nil
		}

		p.Logger.Error("keystone: token validation failed", "status", resp.Status, "subject_token", username, "body", string(bodyBytes))
		return identity, false, fmt.Errorf("keystone login: error %v, body: %s", resp.StatusCode, string(bodyBytes))
	}
	if resp.StatusCode != 201 {
		return identity, false, nil
	}
	token := resp.Header.Get("X-Subject-Token")
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return identity, false, err
	}
	tokenResp := new(tokenResponse)
	err = json.Unmarshal(data, &tokenResp)
	if err != nil {
		return identity, false, fmt.Errorf("keystone: invalid token response: %v", err)
	}
	if scopes.Groups {
		groups, err := p.getUserGroups(ctx, tokenResp.Token.User.ID, token)
		if err != nil {
			return identity, false, err
		}
		identity.Groups = groups
	}
	identity.Username = username
	identity.UserID = tokenResp.Token.User.ID

	user, err := p.getUser(ctx, tokenResp.Token.User.ID, token)
	if err != nil {
		return identity, false, err
	}
	if user.User.Email != "" {
		identity.Email = user.User.Email
		identity.EmailVerified = true
	}

	if p.UserIDKey == "email" && identity.Email != "" {
		identity.UserID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity.Email)).String()
	} else if p.UserIDKey == "username" && identity.Username != "" {
		identity.UserID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity.Username)).String()
	}

	return identity, true, nil
}

func (p *conn) TokenIdentity(ctx context.Context, subjectTokenType, subjectToken string) (connector.Identity, error) {
	var identity connector.Identity

	if p.tokenCache != nil {
		if cached, ok := p.tokenCache.get(subjectToken); ok {
			return cached.(connector.Identity), nil
		}
	}

	// Validate the provided subject token using the connector's admin credentials.
	// Validate the provided subject token using the token itself (self-validation).
	// We use the subjectToken as the X-Auth-Token to validate itself.
	adminToken := subjectToken

	// Ask Keystone to validate the subject token and return token details.
	validateURL := p.Host + "/v3/auth/tokens"
	req, err := http.NewRequest("GET", validateURL, nil)
	if err != nil {
		return identity, err
	}
	req.Header.Set("X-Auth-Token", adminToken)
	req.Header.Set("X-Subject-Token", subjectToken)
	req = req.WithContext(ctx)

	resp, err := p.client.Do(req)
	if err != nil {
		return identity, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		p.Logger.Error("keystone: token validation failed", "status", resp.Status, "subject_token", subjectToken, "body", string(bodyBytes))
		return identity, fmt.Errorf("keystone: token validation failed: %v, body: %s", resp.Status, string(bodyBytes))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return identity, err
	}

	tr := new(tokenResponse)
	if err := json.Unmarshal(data, &tr); err != nil {
		return identity, fmt.Errorf("keystone: invalid token response: %v", err)
	}

	userID := tr.Token.User.ID
	if userID == "" {
		return identity, fmt.Errorf("keystone: token did not contain user id")
	}

	identity.UserID = userID
	identity.Username = tr.Token.User.Name

	// Use admin token to fetch user details (email) and groups.
	user, err := p.getUser(ctx, userID, adminToken)
	if err == nil && user.User.Email != "" {
		identity.Email = user.User.Email
		identity.EmailVerified = true
	}
	groups, err := p.getUserGroups(ctx, userID, adminToken)
	if err == nil {
		identity.Groups = groups
	}

	if p.UserIDKey == "email" && identity.Email != "" {
		identity.UserID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity.Email)).String()
	} else if p.UserIDKey == "username" && identity.Username != "" {
		identity.UserID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity.Username)).String()
	}

	if p.tokenCache != nil {
		p.tokenCache.set(subjectToken, identity)
	}

	return identity, nil
}

func (p *conn) Prompt() string { return "username" }

func (p *conn) Refresh(
	ctx context.Context, scopes connector.Scopes, identity connector.Identity,
) (connector.Identity, error) {
	token, err := p.getAdminToken(ctx)
	if err != nil {
		return identity, fmt.Errorf("keystone: failed to obtain admin token: %v", err)
	}
	ok, err := p.checkIfUserExists(ctx, identity.UserID, token)
	if err != nil {
		return identity, err
	}
	if !ok {
		return identity, fmt.Errorf("keystone: user %q does not exist", identity.UserID)
	}
	if scopes.Groups {
		groups, err := p.getUserGroups(ctx, identity.UserID, token)
		if err != nil {
			return identity, err
		}
		identity.Groups = groups
	}
	return identity, nil
}

func (p *conn) getTokenResponse(ctx context.Context, username, pass string, domain domainKeystone) (response *http.Response, err error) {
	var methods []string
	var pwd *password
	var appCred *applicationCredential
	var totpData *totp

	if strings.HasPrefix(username, "appcred:") {
		methods = []string{"application_credential"}
		appCred = &applicationCredential{
			ID:     strings.TrimPrefix(username, "appcred:"),
			Secret: pass,
		}
	} else {
		methods = []string{"password"}
		pwd = &password{
			User: user{
				Name:     username,
				Domain:   domain,
				Password: pass,
			},
		}
		if code, ok := ctx.Value(TOTPContextKey).(string); ok && code != "" {
			methods = append(methods, "totp")
			totpData = &totp{
				User: userTOTP{
					Name:     username,
					Domain:   domain,
					Passcode: code,
				},
			}
		}
	}

	jsonData := loginRequestData{
		auth: auth{
			Identity: identity{
				Methods:               methods,
				Password:              pwd,
				ApplicationCredential: appCred,
				TOTP:                  totpData,
			},
		},
	}
	jsonValue, err := json.Marshal(jsonData)
	if err != nil {
		return nil, err
	}
	// https://developer.openstack.org/api-ref/identity/v3/#password-authentication-with-unscoped-authorization
	authTokenURL := p.Host + "/v3/auth/tokens/"
	req, err := http.NewRequest("POST", authTokenURL, bytes.NewBuffer(jsonValue))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if receipt, ok := ctx.Value(ReceiptContextKey).(string); ok && receipt != "" {
		req.Header.Set("openstack-auth-receipt", receipt)
	}
	req = req.WithContext(ctx)

	return p.client.Do(req)
}

func (p *conn) getAdminToken(ctx context.Context) (string, error) {
	resp, err := p.getTokenResponse(ctx, p.AdminUsername, p.AdminPassword, p.Domain)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("keystone admin login failed: %v, body: %s", resp.Status, string(bodyBytes))
	}

	token := resp.Header.Get("X-Subject-Token")
	if token == "" {
		return "", fmt.Errorf("keystone admin login: no token returned in X-Subject-Token header")
	}
	io.Copy(io.Discard, resp.Body)
	return token, nil
}

func (p *conn) checkIfUserExists(ctx context.Context, userID string, token string) (bool, error) {
	user, err := p.getUser(ctx, userID, token)
	return user != nil, err
}

func (p *conn) getUser(ctx context.Context, userID string, token string) (*userResponse, error) {
	// https://developer.openstack.org/api-ref/identity/v3/#show-user-details
	userURL := p.Host + "/v3/users/" + userID
	req, err := http.NewRequest("GET", userURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Auth-Token", token)
	req = req.WithContext(ctx)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("keystone: unexpected status %d fetching user %s", resp.StatusCode, userID)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	user := userResponse{}
	err = json.Unmarshal(data, &user)
	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (p *conn) getUserGroups(ctx context.Context, userID string, token string) ([]string, error) {
	// https://developer.openstack.org/api-ref/identity/v3/#list-groups-to-which-a-user-belongs
	groupsURL := p.Host + "/v3/users/" + userID + "/groups"
	req, err := http.NewRequest("GET", groupsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", token)
	req = req.WithContext(ctx)
	resp, err := p.client.Do(req)
	if err != nil {
		p.Logger.Error("error while fetching user groups", "user_id", userID, "err", err)
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	groupsResp := new(groupsResponse)

	err = json.Unmarshal(data, &groupsResp)
	if err != nil {
		return nil, err
	}

	groups := make([]string, len(groupsResp.Groups))
	for i, group := range groupsResp.Groups {
		gName := group.Name
		if mapped, ok := p.groupMap[gName]; ok && mapped != "" {
			gName = mapped
		}
		groups[i] = gName
	}

	if p.fetchRoles {
		roles, err := p.getUserRoles(ctx, userID, token)
		if err == nil {
			groups = append(groups, roles...)
		}
	}

	return groups, nil
}

type roleAssignment struct {
	Role struct {
		Name string `json:"name"`
	} `json:"role"`
	Scope struct {
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
		Domain struct {
			Name string `json:"name"`
		} `json:"domain"`
	} `json:"scope"`
}

type roleAssignmentsResponse struct {
	RoleAssignments []roleAssignment `json:"role_assignments"`
}

func (p *conn) getUserRoles(ctx context.Context, userID string, token string) ([]string, error) {
	// https://docs.openstack.org/api-ref/identity/v3/index.html#list-role-assignments
	rolesURL := p.Host + "/v3/role_assignments?user.id=" + userID + "&include_names=1"
	req, err := http.NewRequest("GET", rolesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", token)
	req = req.WithContext(ctx)
	resp, err := p.client.Do(req)
	if err != nil {
		p.Logger.Error("error while fetching user roles", "user_id", userID, "err", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("keystone roles: unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	rolesResp := new(roleAssignmentsResponse)
	err = json.Unmarshal(data, &rolesResp)
	if err != nil {
		return nil, err
	}

	var roles []string
	seen := make(map[string]bool)

	for _, ra := range rolesResp.RoleAssignments {
		if ra.Role.Name == "" {
			continue
		}
		var roleName string
		if ra.Scope.Project.Name != "" {
			roleName = ra.Role.Name + "@" + ra.Scope.Project.Name
		} else if ra.Scope.Domain.Name != "" {
			roleName = ra.Role.Name + "@" + ra.Scope.Domain.Name
		} else {
			roleName = ra.Role.Name
		}

		if mapped, ok := p.groupMap[roleName]; ok && mapped != "" {
			roleName = mapped
		}

		if !seen[roleName] {
			seen[roleName] = true
			roles = append(roles, roleName)
		}
	}

	return roles, nil
}
