package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ghodss/yaml"

	api "github.com/dexidp/dex/api/v2"
)

// exportBundle is what the backup file contains, shaped like the corresponding
// parts of dex's own config file so it can be read, diffed and largely pasted
// back.
type exportBundle struct {
	ExportedAt string           `json:"exportedAt"`
	ExportedBy string           `json:"exportedBy"`
	DexVersion string           `json:"dexVersion,omitempty"`
	Clients    []exportClient   `json:"staticClients"`
	Connectors []map[string]any `json:"connectors"`
}

type exportClient struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Secret       string   `json:"secret,omitempty"`
	RedirectURIs []string `json:"redirectURIs,omitempty"`
	TrustedPeers []string `json:"trustedPeers,omitempty"`
	Public       bool     `json:"public,omitempty"`
	LogoURL      string   `json:"logoURL,omitempty"`
}

// handleExport writes the clients and connectors out as YAML.
//
// The file contains live credentials: client secrets and whatever the connector
// configs hold. That is the point — a backup without them cannot restore
// anything — but it means this is the one place secrets leave the system, so it
// needs write permission, a confirmation page that says so, and an audit line.
func (d *dashboard) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		d.render(w, r, "confirm.html", page{
			Title: "Export configuration", Nav: "status",
			Data: confirmData{
				Action:  "/export",
				Field:   "confirm",
				Value:   "yes",
				Heading: "Download the configuration of this dex?",
				Warning: "The file contains live credentials: every client secret and whatever the connector configurations hold, such as LDAP bind passwords. Treat it as you would the config file itself. The download is recorded in the audit log with your name.",
				Confirm: "Download YAML",
				Cancel:  "/status",
			},
		})
		return
	}

	ctx := r.Context()
	sess := sessionFrom(ctx)

	clients, err := d.dex.listClients(ctx)
	if err != nil {
		d.logger.Error("export failed listing clients", "err", err)
		d.renderResult(w, r, "clients", friendlyGRPCError(err))
		return
	}

	bundle := exportBundle{
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		ExportedBy: sess.Email,
	}
	if v, err := d.dex.version(ctx); err == nil {
		bundle.DexVersion = v.Server
	}

	// ListClients omits secrets, so each one is fetched individually. A backup
	// that cannot restore a confidential client is not a backup.
	for _, c := range clients {
		full, err := d.dex.getClient(ctx, c.Id)
		if err != nil || full == nil {
			d.logger.Warn("export could not read a client's secret", "client_id", c.Id, "err", err)
			full = &api.Client{
				Id: c.Id, Name: c.Name, Public: c.Public,
				RedirectUris: c.RedirectUris, TrustedPeers: c.TrustedPeers, LogoUrl: c.LogoUrl,
			}
		}
		bundle.Clients = append(bundle.Clients, exportClient{
			ID: full.Id, Name: full.Name, Secret: full.Secret,
			RedirectURIs: full.RedirectUris, TrustedPeers: full.TrustedPeers,
			Public: full.Public, LogoURL: full.LogoUrl,
		})
	}

	// Connectors are optional: the listing is gated behind a feature flag, and
	// an export of the clients alone still beats nothing.
	if connectors, err := d.dex.listConnectors(ctx); err == nil {
		for _, c := range connectors {
			entry := map[string]any{"id": c.Id, "type": c.Type, "name": c.Name}
			var cfg any
			if err := json.Unmarshal(c.Config, &cfg); err == nil {
				entry["config"] = cfg
			} else {
				entry["config"] = string(c.Config)
			}
			bundle.Connectors = append(bundle.Connectors, entry)
		}
	} else {
		d.logger.Warn("export could not read connectors", "err", err)
	}

	out, err := yaml.Marshal(bundle)
	if err != nil {
		d.logger.Error("export failed to marshal", "err", err)
		http.Error(w, "Could not build the export.", http.StatusInternalServerError)
		return
	}

	d.logger.Info("configuration exported", "actor", sess.Email,
		"clients", len(bundle.Clients), "connectors", len(bundle.Connectors))

	filename := fmt.Sprintf("dex-config-%s.yaml", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Write(out)
}

// handleDiscovery shows dex's OIDC discovery document: the endpoints, scopes and
// claims someone integrating an application has to be told.
func (d *dashboard) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	d.renderList(w, r, "discovery.html", "Discovery", "discovery", func() (any, error) {
		return d.dex.api.GetDiscovery(d.dex.authed(r.Context()), &api.DiscoveryReq{})
	})
}
