package main

import (
	"context"
	"fmt"

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
