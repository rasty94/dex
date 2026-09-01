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

// authed attaches the admin token. The dashboard is the only thing that knows
// it; browsers never see it.
func (d *dexClient) authed(ctx context.Context) context.Context {
	if d.token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+d.token)
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

// ponytail: the refresh API keys on the "sub" claim, which is a base64 protobuf
// of (userID, connectorID) built by server/internal — a package cmd/ cannot
// import, and whose wire format is not worth duplicating here. The sessions
// view therefore asks for a sub rather than deriving one; an operator chasing
// an incident has the token in front of them. Expose an encoder from dex if
// looking a user up by name ever becomes the common case.
