// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
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

	store     *Store
	router    *mux.Router
	botID     string
	reminders *reminderService
	job       *cluster.Job

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

	p.botID, err = p.API.EnsureBotUser(&model.Bot{
		Username:    "calendar",
		DisplayName: "Calendar",
		Description: "Reminds you of your events.",
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure the Calendar bot")
	}

	// One server of the cluster sends the reminders
	p.reminders = newReminderService(p)
	p.job, err = cluster.Schedule(p.API, "reminders", cluster.MakeWaitForInterval(reminderTick), func() {
		p.reminders.run(time.Now())
	})
	if err != nil {
		return errors.Wrap(err, "failed to schedule the reminders")
	}
	return nil
}

// OnDeactivate is invoked when the plugin is deactivated.
func (p *Plugin) OnDeactivate() error {
	if p.job != nil {
		if err := p.job.Close(); err != nil {
			p.API.LogWarn("Failed to stop the reminders", "err", err.Error())
		}
		p.job = nil
	}
	return nil
}

// ServeHTTP serves the plugin's REST API.
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.router.ServeHTTP(w, r)
}
