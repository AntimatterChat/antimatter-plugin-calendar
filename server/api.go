// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"
	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	requestBodyMaxSizeBytes = 256 * 1024
	maxConcurrentQueries    = 4
)

// ClientConfig is the part of the plugin configuration clients need.
type ClientConfig struct {
	RemindersEnabled         bool `json:"reminders_enabled"`
	AllowInsecureConnections bool `json:"allow_insecure_connections"`
}

// accountView is an account as shown to its user, without the password.
type accountView struct {
	URL           string `json:"url"`
	Username      string `json:"username"`
	RemindMinutes int    `json:"remind_minutes"`
}

func newAccountView(a *Account) *accountView {
	if a == nil {
		return nil
	}
	return &accountView{URL: a.URL, Username: a.Username, RemindMinutes: a.RemindMinutes}
}

// accountRequest saves an account. An empty password keeps the stored one.
type accountRequest struct {
	accountView
	Password string `json:"password"`
}

// EventDetails is an event as the event editor shows it: the whole series of a repeating event.
type EventDetails struct {
	Calendar    string     `json:"calendar"`
	Path        string     `json:"path"`
	ETag        string     `json:"etag"`
	Summary     string     `json:"summary"`
	Description string     `json:"description"`
	Location    string     `json:"location"`
	AllDay      bool       `json:"all_day"`
	Start       string     `json:"start"`
	End         string     `json:"end"`
	TimeZone    string     `json:"time_zone"`
	Recurrence  string     `json:"recurrence"`
	Alarm       *int       `json:"alarm"`
	Link        *EventLink `json:"link,omitempty"`
}

// calendarError is a calendar whose events couldn't be read.
type calendarError struct {
	Calendar string `json:"calendar"`
	Error    string `json:"error"`
}

// newRouter returns the router of the plugin's REST API, served under
// /plugins/com.antimatterchat.calendar/api/v1.
func (p *Plugin) newRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(p.requireUser)

	api := router.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/config", p.handleGetConfig).Methods(http.MethodGet)
	api.HandleFunc("/account", p.handleGetAccount).Methods(http.MethodGet)
	api.HandleFunc("/account", p.handleSaveAccount).Methods(http.MethodPut)
	api.HandleFunc("/account", p.handleDeleteAccount).Methods(http.MethodDelete)
	api.HandleFunc("/calendars", p.handleListCalendars).Methods(http.MethodGet)
	api.HandleFunc("/events", p.handleListEvents).Methods(http.MethodGet)
	api.HandleFunc("/events", p.handleCreateEvent).Methods(http.MethodPost)
	api.HandleFunc("/event", p.handleGetEvent).Methods(http.MethodGet)
	api.HandleFunc("/event", p.handleUpdateEvent).Methods(http.MethodPut)
	api.HandleFunc("/event", p.handleDeleteEvent).Methods(http.MethodDelete)

	return router
}

func (p *Plugin) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mattermost-User-Id") == "" {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Plugin) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		p.API.LogWarn("Failed to write response", "err", err.Error())
	}
}

func (p *Plugin) writeError(w http.ResponseWriter, err error) {
	status := statusFor(err)
	message := err.Error()
	if status >= http.StatusInternalServerError && status != http.StatusBadGateway {
		p.API.LogError("Calendar request failed", "err", err.Error())
		message = "something went wrong, try again later"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, requestBodyMaxSizeBytes)).Decode(v); err != nil {
		return newAPIError(http.StatusBadRequest, "invalid request body")
	}
	return nil
}

func userID(r *http.Request) string {
	return r.Header.Get("Mattermost-User-Id")
}

func (p *Plugin) davOptions() davOptions {
	cfg := p.getConfiguration()
	return davOptions{dialer: newDialer(cfg.AllowPrivateNetworks), allowInsecure: cfg.AllowInsecureConnections, tlsConfig: p.tlsConfig}
}

var errNoAccount = newAPIError(http.StatusConflict, "connect a calendar account first")

// session is a user's connection to their calendar server.
type session struct {
	account *Account
	client  *davClient
	homeSet *url.URL
}

func (p *Plugin) openSession(userID string) (*session, error) {
	account, err := p.store.GetAccount(userID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, errNoAccount
	}
	homeSet, err := url.Parse(account.HomeSetURL)
	if err != nil || homeSet.Host == "" {
		return nil, newAPIError(http.StatusConflict, "connect your calendar account again")
	}
	return &session{account: account, client: newDAVClient(account.Username, account.Password, p.davOptions()), homeSet: homeSet}, nil
}

// validObjectPath checks that a path is that of a calendar object in the user's home set.
func (s *session) validObjectPath(objectPath string) error {
	if !strings.HasPrefix(objectPath, s.homeSet.Path) || strings.Contains(objectPath, "..") || !strings.HasSuffix(objectPath, ".ics") {
		return newAPIError(http.StatusBadRequest, "invalid event path")
	}
	return nil
}

func (s *session) validCalendar(calendar string) error {
	if !strings.HasPrefix(calendar, s.homeSet.Path) || strings.Contains(calendar, "..") || !strings.HasSuffix(calendar, "/") {
		return newAPIError(http.StatusBadRequest, "invalid calendar")
	}
	return nil
}

func (p *Plugin) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := p.getConfiguration()
	p.writeJSON(w, ClientConfig{RemindersEnabled: cfg.EnableReminders, AllowInsecureConnections: cfg.AllowInsecureConnections})
}

func (p *Plugin) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	account, err := p.store.GetAccount(userID(r))
	if err != nil {
		p.writeError(w, err)
		return
	}
	p.writeJSON(w, map[string]any{"account": newAccountView(account)})
}

func (p *Plugin) handleSaveAccount(w http.ResponseWriter, r *http.Request) {
	var req accountRequest
	if err := readJSON(w, r, &req); err != nil {
		p.writeError(w, err)
		return
	}
	existing, err := p.store.GetAccount(userID(r))
	if err != nil {
		// An account that can't be decrypted any more is replaced
		existing = nil
	}

	account := &Account{URL: strings.TrimSpace(req.URL), Username: strings.TrimSpace(req.Username), Password: req.Password, RemindMinutes: req.RemindMinutes}
	if account.Password == "" && existing != nil {
		account.Password = existing.Password
	}
	if err := account.Validate(); err != nil {
		p.writeError(w, err)
		return
	}

	// Find the calendars before saving
	ctx, cancel := context.WithTimeout(r.Context(), 2*requestTimeout)
	defer cancel()
	client := newDAVClient(account.Username, account.Password, p.davOptions())
	homeSet, err := client.discover(ctx, account.URL)
	if err != nil {
		p.writeError(w, err)
		return
	}
	if _, err := client.listCalendars(ctx, homeSet); err != nil {
		p.writeError(w, err)
		return
	}
	account.HomeSetURL = homeSet.String()

	if err := p.store.SaveAccount(userID(r), account); err != nil {
		p.writeError(w, err)
		return
	}
	p.eventsChanged(userID(r))
	p.writeJSON(w, map[string]any{"account": newAccountView(account)})
}

func (p *Plugin) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := p.store.DeleteAccount(userID(r)); err != nil {
		p.writeError(w, err)
		return
	}
	p.eventsChanged(userID(r))
	p.writeJSON(w, map[string]any{"account": nil})
}

func (p *Plugin) handleListCalendars(w http.ResponseWriter, r *http.Request) {
	s, err := p.openSession(userID(r))
	if err != nil {
		p.writeError(w, err)
		return
	}
	calendars, err := s.client.listCalendars(r.Context(), s.homeSet)
	if err != nil {
		p.writeError(w, err)
		return
	}
	p.writeJSON(w, calendars)
}

func queryLocation(r *http.Request) (*time.Location, error) {
	name := r.URL.Query().Get("tz")
	if name == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, newAPIError(http.StatusBadRequest, "unknown time zone")
	}
	return loc, nil
}

// listOccurrences returns the occurrences of the events of some calendars between from and to,
// and the calendars that couldn't be read.
func (s *session) listOccurrences(ctx context.Context, calendars []string, from, to time.Time, loc *time.Location) ([]Occurrence, []calendarError) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	occurrences := []Occurrence{}
	failed := []calendarError{}
	slots := make(chan struct{}, maxConcurrentQueries)

	for _, calendar := range calendars {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			objects, err := s.client.queryEvents(ctx, s.homeSet, calendar, from, to)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, calendarError{Calendar: calendar, Error: err.Error()})
				return
			}
			for i := range objects {
				if objects[i].Data != nil {
					occurrences = append(occurrences, expandObject(calendar, objects[i].Path, objects[i].ETag, objects[i].Data, from, to, loc)...)
				}
			}
		}()
	}
	wg.Wait()
	sortOccurrences(occurrences)
	return occurrences, failed
}

func (p *Plugin) handleListEvents(w http.ResponseWriter, r *http.Request) {
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid start of the time range"))
		return
	}
	to, err := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err != nil || !to.After(from) {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid end of the time range"))
		return
	}
	loc, err := queryLocation(r)
	if err != nil {
		p.writeError(w, err)
		return
	}

	s, err := p.openSession(userID(r))
	if err != nil {
		p.writeError(w, err)
		return
	}
	var calendars []string
	if list := r.URL.Query().Get("calendars"); list != "" {
		calendars = strings.Split(list, ",")
		for _, calendar := range calendars {
			if err := s.validCalendar(calendar); err != nil {
				p.writeError(w, err)
				return
			}
		}
	} else {
		all, err := s.client.listCalendars(r.Context(), s.homeSet)
		if err != nil {
			p.writeError(w, err)
			return
		}
		for _, calendar := range all {
			calendars = append(calendars, calendar.ID)
		}
	}

	occurrences, failed := s.listOccurrences(r.Context(), calendars, from, to, loc)
	p.fillLinks(userID(r), occurrences)
	p.writeJSON(w, map[string]any{"events": occurrences, "errors": failed})
}

// fillLinks adds the names of the channels events are linked to, when the user can read them.
func (p *Plugin) fillLinks(userID string, occurrences []Occurrence) {
	links := map[string]*EventLink{}
	for i := range occurrences {
		link := occurrences[i].Link
		if link == nil {
			continue
		}
		filled, ok := links[link.ChannelID]
		if !ok {
			filled = p.channelLink(userID, link.ChannelID)
			links[link.ChannelID] = filled
		}
		if filled != nil {
			link.ChannelName, link.DisplayName, link.ChannelType, link.TeamName = filled.ChannelName, filled.DisplayName, filled.ChannelType, filled.TeamName
		}
	}
}

// channelLink returns a link to a channel the user can read, nil if they can't.
func (p *Plugin) channelLink(userID, channelID string) *EventLink {
	if !model.IsValidId(channelID) || !p.API.HasPermissionToChannel(userID, channelID, model.PermissionReadChannel) {
		return nil
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return nil
	}
	link := &EventLink{ChannelID: channel.Id, ChannelName: channel.Name, DisplayName: channel.DisplayName, ChannelType: string(channel.Type)}
	if channel.TeamId != "" {
		if team, appErr := p.API.GetTeam(channel.TeamId); appErr == nil {
			link.TeamName = team.Name
		}
	}
	return link
}

// eventsChanged makes the reminders read the events of a user again.
func (p *Plugin) eventsChanged(userID string) {
	if p.reminders != nil {
		p.reminders.invalidate(userID)
	}
}

// permalink returns the URL of a channel, for other calendar clients.
func (p *Plugin) permalink(link *EventLink) string {
	if link == nil || link.TeamName == "" || link.ChannelName == "" {
		return ""
	}
	siteURL := ""
	if cfg := p.API.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		siteURL = strings.TrimSuffix(*cfg.ServiceSettings.SiteURL, "/")
	}
	if siteURL == "" {
		return ""
	}
	return siteURL + "/" + link.TeamName + "/channels/" + link.ChannelName
}

// checkLink checks that the user can read the channel they link an event to, and returns the
// channel's permalink.
func (p *Plugin) checkLink(userID string, in *EventInput) (string, error) {
	if in.Link == nil || in.Link.ChannelID == "" {
		in.Link = nil
		return "", nil
	}
	link := p.channelLink(userID, in.Link.ChannelID)
	if link == nil {
		return "", newAPIError(http.StatusForbidden, "you can't link this event to that channel")
	}
	return p.permalink(link), nil
}

func (p *Plugin) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var in EventInput
	if err := readJSON(w, r, &in); err != nil {
		p.writeError(w, err)
		return
	}
	if err := in.validate(); err != nil {
		p.writeError(w, err)
		return
	}
	s, err := p.openSession(userID(r))
	if err != nil {
		p.writeError(w, err)
		return
	}
	if err := s.validCalendar(in.Calendar); err != nil {
		p.writeError(w, err)
		return
	}
	permalink, err := p.checkLink(userID(r), &in)
	if err != nil {
		p.writeError(w, err)
		return
	}

	uid := model.NewId()
	cal, err := newEventObject(&in, uid, time.Now(), permalink)
	if err != nil {
		p.writeError(w, err)
		return
	}
	objectPath := in.Calendar + uid + ".ics"
	etag, err := s.client.putObject(r.Context(), s.homeSet, objectPath, cal, "")
	if err != nil {
		p.writeError(w, err)
		return
	}
	p.eventsChanged(userID(r))
	p.writeJSON(w, map[string]string{"path": objectPath, "etag": etag})
}

func (p *Plugin) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	loc, err := queryLocation(r)
	if err != nil {
		p.writeError(w, err)
		return
	}
	s, err := p.openSession(userID(r))
	if err != nil {
		p.writeError(w, err)
		return
	}
	objectPath := r.URL.Query().Get("path")
	if err := s.validObjectPath(objectPath); err != nil {
		p.writeError(w, err)
		return
	}
	object, err := s.client.getObject(r.Context(), s.homeSet, objectPath)
	if err != nil {
		p.writeError(w, err)
		return
	}
	details, err := eventDetails(object.Data, loc)
	if err != nil {
		p.writeError(w, err)
		return
	}
	details.Path, details.ETag = objectPath, object.ETag
	details.Calendar = objectPath[:strings.LastIndex(objectPath, "/")+1]
	if details.Link != nil {
		if filled := p.channelLink(userID(r), details.Link.ChannelID); filled != nil {
			filled.Kind = details.Link.Kind
			details.Link = filled
		}
	}
	p.writeJSON(w, details)
}

// eventDetails returns the event of an object as the editor shows it.
func eventDetails(cal *ical.Calendar, loc *time.Location) (*EventDetails, error) {
	ev := masterEvent(cal)
	if ev == nil {
		return nil, errNoEvent
	}
	start, end, allDay, err := eventTimes(ev, loc)
	if err != nil {
		return nil, newAPIError(http.StatusUnprocessableEntity, "this event can't be read")
	}

	details := &EventDetails{
		Summary:     textProp(ev, ical.PropSummary),
		Description: textProp(ev, ical.PropDescription),
		Location:    textProp(ev, ical.PropLocation),
		AllDay:      allDay,
		TimeZone:    loc.String(),
		Link:        eventLink(ev),
	}
	if allDay {
		details.Start, details.End = start.Format(dateFormat), end.Format(dateFormat)
	} else {
		details.Start, details.End = start.Format(time.RFC3339), end.Format(time.RFC3339)
		if start.Location() != time.UTC {
			details.TimeZone = start.Location().String()
		}
	}
	if rule, err := ev.Props.RecurrenceRule(); err == nil {
		details.Recurrence = recurrenceKind(rule)
	} else {
		details.Recurrence = RecurrenceCustom
	}
	if minutes := alarms(ev, start, end, loc); len(minutes) > 0 {
		details.Alarm = &minutes[0]
	}
	return details, nil
}

func (p *Plugin) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	var in EventInput
	if err := readJSON(w, r, &in); err != nil {
		p.writeError(w, err)
		return
	}
	if err := in.validate(); err != nil {
		p.writeError(w, err)
		return
	}
	s, err := p.openSession(userID(r))
	if err != nil {
		p.writeError(w, err)
		return
	}
	objectPath := r.URL.Query().Get("path")
	if err := s.validObjectPath(objectPath); err != nil {
		p.writeError(w, err)
		return
	}
	permalink, err := p.checkLink(userID(r), &in)
	if err != nil {
		p.writeError(w, err)
		return
	}

	object, err := s.client.getObject(r.Context(), s.homeSet, objectPath)
	if err != nil {
		p.writeError(w, err)
		return
	}
	if etag := r.URL.Query().Get("etag"); etag != "" && etag != object.ETag {
		p.writeError(w, errChanged)
		return
	}
	if err := updateEventObject(object.Data, &in, time.Now(), permalink); err != nil {
		p.writeError(w, err)
		return
	}
	etag, err := s.client.putObject(r.Context(), s.homeSet, objectPath, object.Data, object.ETag)
	if err != nil {
		p.writeError(w, err)
		return
	}
	p.eventsChanged(userID(r))
	p.writeJSON(w, map[string]string{"path": objectPath, "etag": etag})
}

// handleDeleteEvent deletes an event, or one occurrence of a repeating event (occurrence is its
// recurrence ID, in milliseconds).
func (p *Plugin) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	s, err := p.openSession(userID(r))
	if err != nil {
		p.writeError(w, err)
		return
	}
	objectPath := r.URL.Query().Get("path")
	if err := s.validObjectPath(objectPath); err != nil {
		p.writeError(w, err)
		return
	}
	etag := r.URL.Query().Get("etag")

	occurrence := r.URL.Query().Get("occurrence")
	if occurrence == "" {
		if err := s.client.deleteObject(r.Context(), s.homeSet, objectPath, etag); err != nil {
			p.writeError(w, err)
			return
		}
		p.eventsChanged(userID(r))
		p.writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	ms, err := strconv.ParseInt(occurrence, 10, 64)
	if err != nil {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid occurrence"))
		return
	}
	loc, err := queryLocation(r)
	if err != nil {
		p.writeError(w, err)
		return
	}
	object, err := s.client.getObject(r.Context(), s.homeSet, objectPath)
	if err != nil {
		p.writeError(w, err)
		return
	}
	if etag != "" && etag != object.ETag {
		p.writeError(w, errChanged)
		return
	}
	if err := excludeOccurrence(object.Data, time.UnixMilli(ms).In(loc), loc, time.Now()); err != nil {
		p.writeError(w, err)
		return
	}
	if _, err := s.client.putObject(r.Context(), s.homeSet, objectPath, object.Data, object.ETag); err != nil {
		p.writeError(w, err)
		return
	}
	p.eventsChanged(userID(r))
	p.writeJSON(w, map[string]string{"status": "ok"})
}
