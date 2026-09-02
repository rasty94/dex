package main

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	api "github.com/dexidp/dex/api/v2"
)

// dexClient wraps the gRPC API. It owns the admin token and attaches it to
// every call, so no handler above it ever has to hold the credential.
type dexClient struct {
	api   api.DexClient
	token string
	conn  *grpc.ClientConn
}

func newDexClient(c DexConfig) (*dexClient, error) {
	creds := insecure.NewCredentials()
	if c.CACert != "" {
		tc, err := credentials.NewClientTLSFromFile(c.CACert, "")
		if err != nil {
			return nil, fmt.Errorf("load dex.caCert: %w", err)
		}
		creds = tc
	}

	conn, err := grpc.NewClient(c.GRPCAddress, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dial dex gRPC API: %w", err)
	}
	return &dexClient{api: api.NewDexClient(conn), token: c.Token, conn: conn}, nil
}

func (d *dexClient) Close() error { return d.conn.Close() }

// ActorHeader names the administrator on whose behalf a call is made. dex's API
// authenticates the token, not the person, so its log would otherwise only ever
// say "the token did it". The dashboard is already fully trusted by that token;
// this is it attesting who asked, so the audit trail names a human.
const ActorHeader = "x-dex-actor"

// authed attaches the admin token and, when the call is on behalf of a
// signed-in administrator, their identity. The token never leaves this process;
// browsers only ever see an opaque session id.
func (d *dexClient) authed(ctx context.Context) context.Context {
	if d.token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+d.token)
	}
	if sess := sessionFrom(ctx); sess != nil && sess.Email != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, ActorHeader, sess.Email)
	}
	return ctx
}

func (d *dexClient) listClients(ctx context.Context) ([]*api.ClientInfo, error) {
	resp, err := d.api.ListClients(d.authed(ctx), &api.ListClientReq{})
	if err != nil {
		return nil, err
	}
	return resp.Clients, nil
}

func (d *dexClient) listConnectors(ctx context.Context) ([]*api.Connector, error) {
	resp, err := d.api.ListConnectors(d.authed(ctx), &api.ListConnectorReq{})
	if err != nil {
		return nil, err
	}
	return resp.Connectors, nil
}

func (d *dexClient) listPasswords(ctx context.Context) ([]*api.Password, error) {
	resp, err := d.api.ListPasswords(d.authed(ctx), &api.ListPasswordReq{})
	if err != nil {
		return nil, err
	}
	return resp.Passwords, nil
}

func (d *dexClient) version(ctx context.Context) (*api.VersionResp, error) {
	return d.api.GetVersion(d.authed(ctx), &api.VersionReq{})
}

func (d *dexClient) listRefresh(ctx context.Context, subject string) ([]*api.RefreshTokenRef, error) {
	resp, err := d.api.ListRefresh(d.authed(ctx), &api.ListRefreshReq{UserId: subject})
	if err != nil {
		return nil, err
	}
	return resp.RefreshTokens, nil
}

func (d *dexClient) revokeRefresh(ctx context.Context, subject, clientID string) (notFound bool, err error) {
	resp, err := d.api.RevokeRefresh(d.authed(ctx), &api.RevokeRefreshReq{UserId: subject, ClientId: clientID})
	if err != nil {
		return false, err
	}
	return resp.NotFound, nil
}

// revokeAllRefresh cuts every refresh token a user holds. It reports how many
// it revoked, and returns the first failure with what it had already done, so
// the caller can say "revoked 3 of 5" instead of pretending nothing happened.
func (d *dexClient) revokeAllRefresh(ctx context.Context, subject string) (revoked int, err error) {
	tokens, err := d.listRefresh(ctx, subject)
	if err != nil {
		return 0, err
	}
	for _, t := range tokens {
		notFound, err := d.revokeRefresh(ctx, subject, t.ClientId)
		if err != nil {
			return revoked, err
		}
		if !notFound {
			revoked++
		}
	}
	return revoked, nil
}

// listAuthSessions returns the browser sessions of a user on one connector.
// These are not the same thing as refresh tokens: a session is one signed-in
// browser, a refresh token is one application's long-lived grant. Ending a
// session does not revoke the tokens already issued from it.
func (d *dexClient) listAuthSessions(ctx context.Context, userID, connID string) ([]*api.AuthSession, error) {
	resp, err := d.api.ListAuthSessions(d.authed(ctx), &api.ListAuthSessionsReq{
		UserId:      userID,
		ConnectorId: connID,
	})
	if err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

// deleteAuthSession ends one browser session and revokes the refresh tokens
// that came from it.
func (d *dexClient) deleteAuthSession(ctx context.Context, id string) error {
	_, err := d.api.DeleteAuthSession(d.authed(ctx), &api.DeleteAuthSessionReq{Id: id})
	return err
}

// terminateSessionsByUser ends every browser session a user has, on every
// connector, and reports how many it ended.
func (d *dexClient) terminateSessionsByUser(ctx context.Context, userID string) (int64, error) {
	resp, err := d.api.TerminateSessionsByUser(d.authed(ctx), &api.TerminateSessionsByUserReq{UserId: userID})
	if err != nil {
		return 0, err
	}
	return resp.SessionsTerminated, nil
}

// getUserIdentity returns what dex knows about a user on one connector: the
// claims it last saw, what the user consented to and for which client, any
// enrolled second factors, and whether the account is locked out.
func (d *dexClient) getUserIdentity(ctx context.Context, userID, connID string) (*api.UserIdentity, error) {
	resp, err := d.api.GetUserIdentity(d.authed(ctx), &api.GetUserIdentityReq{
		UserId:      userID,
		ConnectorId: connID,
	})
	if err != nil {
		return nil, err
	}
	return resp.Identity, nil
}

// revokeConsent withdraws a user's approval for one client, so the next login
// through it shows the consent screen again. It does not sign the user out.
func (d *dexClient) revokeConsent(ctx context.Context, userID, connID, clientID string) (notFound bool, err error) {
	resp, err := d.api.RevokeConsent(d.authed(ctx), &api.RevokeConsentReq{
		UserId:      userID,
		ConnectorId: connID,
		ClientId:    clientID,
	})
	if err != nil {
		return false, err
	}
	return resp.NotFound, nil
}

// terminateSessionsByConnector signs out everyone who authenticated through one
// connector. This is the button for retiring an identity provider.
func (d *dexClient) terminateSessionsByConnector(ctx context.Context, connID string) (int64, error) {
	resp, err := d.api.TerminateSessionsByConnector(d.authed(ctx), &api.TerminateSessionsByConnectorReq{ConnectorId: connID})
	if err != nil {
		return 0, err
	}
	return resp.SessionsTerminated, nil
}

// deleteUserIdentity purges an identity and everything hanging off it. This is
// the GDPR erasure call and it does not come back.
func (d *dexClient) deleteUserIdentity(ctx context.Context, userID, connID string) (notFound bool, err error) {
	resp, err := d.api.DeleteUserIdentity(d.authed(ctx), &api.DeleteUserIdentityReq{
		UserId:      userID,
		ConnectorId: connID,
	})
	if err != nil {
		return false, err
	}
	return resp.NotFound, nil
}

// localPasswordFor finds the local password account that shares an email with
// an identity, or nil. dex's password store is keyed by email with no connector
// attached, so purging an identity from any connector deletes the local account
// that happens to use the same address — a consequence the purge's name does not
// suggest, and the reason the confirmation has to spell it out.
func (d *dexClient) localPasswordFor(ctx context.Context, email string) (*api.Password, error) {
	if email == "" {
		return nil, nil
	}
	passwords, err := d.listPasswords(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range passwords {
		if strings.EqualFold(p.Email, email) {
			return p, nil
		}
	}
	return nil, nil
}

func (d *dexClient) createClient(ctx context.Context, c *api.Client) (alreadyExists bool, err error) {
	resp, err := d.api.CreateClient(d.authed(ctx), &api.CreateClientReq{Client: c})
	if err != nil {
		return false, err
	}
	return resp.AlreadyExists, nil
}

func (d *dexClient) updateClient(ctx context.Context, req *api.UpdateClientReq) (notFound bool, err error) {
	resp, err := d.api.UpdateClient(d.authed(ctx), req)
	if err != nil {
		return false, err
	}
	return resp.NotFound, nil
}

func (d *dexClient) deleteClient(ctx context.Context, id string) (notFound bool, err error) {
	resp, err := d.api.DeleteClient(d.authed(ctx), &api.DeleteClientReq{Id: id})
	if err != nil {
		return false, err
	}
	return resp.NotFound, nil
}

func (d *dexClient) getClient(ctx context.Context, id string) (*api.Client, error) {
	resp, err := d.api.GetClient(d.authed(ctx), &api.GetClientReq{Id: id})
	if err != nil {
		return nil, err
	}
	return resp.Client, nil
}

func (d *dexClient) createConnector(ctx context.Context, c *api.Connector) (alreadyExists bool, err error) {
	resp, err := d.api.CreateConnector(d.authed(ctx), &api.CreateConnectorReq{Connector: c})
	if err != nil {
		return false, err
	}
	return resp.AlreadyExists, nil
}

func (d *dexClient) updateConnector(ctx context.Context, req *api.UpdateConnectorReq) (notFound bool, err error) {
	resp, err := d.api.UpdateConnector(d.authed(ctx), req)
	if err != nil {
		return false, err
	}
	return resp.NotFound, nil
}

func (d *dexClient) deleteConnector(ctx context.Context, id string) (notFound bool, err error) {
	resp, err := d.api.DeleteConnector(d.authed(ctx), &api.DeleteConnectorReq{Id: id})
	if err != nil {
		return false, err
	}
	return resp.NotFound, nil
}

// connector returns one connector by id. The API has no Get, so this filters
// the listing; there are never many connectors.
func (d *dexClient) connector(ctx context.Context, id string) (*api.Connector, error) {
	conns, err := d.listConnectors(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		if c.Id == id {
			return c, nil
		}
	}
	return nil, nil
}

func (d *dexClient) reloadConfig(ctx context.Context) (string, error) {
	resp, err := d.api.ReloadConfig(d.authed(ctx), &api.ReloadConfigReq{})
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("%s", resp.Error)
	}
	return "Configuration reloaded.", nil
}

func (d *dexClient) createPassword(ctx context.Context, p *api.Password) (alreadyExists bool, err error) {
	resp, err := d.api.CreatePassword(d.authed(ctx), &api.CreatePasswordReq{Password: p})
	if err != nil {
		return false, err
	}
	return resp.AlreadyExists, nil
}

func (d *dexClient) updatePassword(ctx context.Context, req *api.UpdatePasswordReq) (notFound bool, err error) {
	resp, err := d.api.UpdatePassword(d.authed(ctx), req)
	if err != nil {
		return false, err
	}
	return resp.NotFound, nil
}

func (d *dexClient) deletePassword(ctx context.Context, email string) (notFound bool, err error) {
	resp, err := d.api.DeletePassword(d.authed(ctx), &api.DeletePasswordReq{Email: email})
	if err != nil {
		return false, err
	}
	return resp.NotFound, nil
}

// ponytail: the refresh API keys on the "sub" claim, which is a base64 protobuf
// of (userID, connectorID) built by server/internal — a package cmd/ cannot
// import, and whose wire format is not worth duplicating here. The sessions
// view therefore asks for a sub rather than deriving one; an operator chasing
// an incident has the token in front of them. Expose an encoder from dex if
// looking a user up by name ever becomes the common case.
