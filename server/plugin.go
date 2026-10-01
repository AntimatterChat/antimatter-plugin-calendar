// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"crypto/tls"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

// Plugin implements the interface expected by the Antimatter server to communicate between the
// server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration

	store  *Store
	router *mux.Router

	// tlsConfig is nil for the default TLS configuration (tests set their own CA).
	tlsConfig *tls.Config
}

// OnActivate is invoked when the plugin is activated.
func (p *Plugin) OnActivate() error {
	key, err := loadEncryptionKey(p.API)
	if err != nil {
		return err
	}
	box, err := newSecretBox(key)
	if err != nil {
		return errors.Wrap(err, "failed to set up the encryption")
	}

	p.store = NewStore(p.API, box)
	p.router = p.newRouter()
	return nil
}

// ServeHTTP serves the plugin's REST API.
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.router.ServeHTTP(w, r)
}
