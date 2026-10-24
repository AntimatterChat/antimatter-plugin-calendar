# Scheduled calls: the Calendar app's API

The routes the web app and the mobile app use for calls scheduled in a channel (F292). Everything is under
`/plugins/com.antimatterchat.calendar/api/v1`, with the person's own session (the web app's cookie and CSRF header,
or the mobile app's `Authorization: Bearer <token>`); the server passes their user ID to the app, and every route
answers `401` without one. The one exception is the calendar feed (below), read by calendar apps with its own token.

Times are milliseconds since the epoch (UTC); a call's `start` and `end` are kept to the whole second. IDs are the
server's 26-character IDs. An error is `{"error": "<message>"}` with the status given below; the message is English,
for logs, not for display.

## The meeting

A call scheduled for everyone in a channel. Its post in the channel (type `custom_calendar_meeting`, prop
`meeting_id`) is posted by the Calendar bot; clients draw it from this object, read with `GET /meetings/{id}`.

```json
{
  "id": "k3w1x9...",
  "channel_id": "c5f8...",
  "team_id": "t9a2...",
  "post_id": "p7d4...",
  "organizer_id": "u1b6...",
  "summary": "Weekly sync",
  "description": "Agenda in the channel header",
  "start": 1793541600000,
  "end": 1793543400000,
  "time_zone": "Europe/Paris",
  "recurrence_rule": "FREQ=WEEKLY;BYDAY=MO,TH;COUNT=10",
  "exception_dates": [1793800800000],
  "posted_occurrence_start": 1793800800000,
  "posted_occurrence_end": 1793802600000,
  "next_start": 1794146400000,
  "next_end": 1794148200000,
  "reminder_minutes": 10,
  "my_reminder_override": 60,
  "sequence": 3,
  "cancelled": false,
  "created_at": 1793000000000,
  "updated_at": 1793100000000,
  "rsvps": {"u1b6...": {"status": "going", "updated_at": 1793000001000}},
  "next_rsvps": {"u1b6...": {"status": "no", "updated_at": 1793000002000, "occurrence_start": 1794146400000}},
  "my_rsvp": "going",
  "my_rsvp_next_only": false,
  "can_cancel": true
}
```

- `start`/`end`: the first occurrence of a series (the only one of a one-off call). Every occurrence lasts `end - start`.
- `time_zone`, `recurrence_rule`, `exception_dates`: absent for a one-off call. `recurrence_rule` is an RFC 5545
  RRULE; the web app writes only `FREQ` (`DAILY`, `WEEKLY`, `MONTHLY`), `INTERVAL`, `BYDAY` (weekly, plain
  `MO`…`SU`), and `UNTIL` (UTC, `20261231T225959Z`) or `COUNT`. The server also accepts `YEARLY` and any other RRULE
  part, but never more often than daily, at most 500 occurrences, and an `UNTIL` at most 2 years after `start`.
  Occurrences are expanded in `time_zone` (UTC when absent). `exception_dates` are the starts of cancelled
  occurrences.
- `next_start`/`next_end`: the occurrence live or next now, computed by the server; absent once the call or the
  series is over, and for a cancelled one. Show this time on the post; RSVP "next only" and "cancel this call"
  apply to it.
- `posted_occurrence_*`: the occurrence the post's text currently names (server bookkeeping; clients needn't use it).
- `reminder_minutes`: the call's own reminder, one of `-1` (none), `0` (at start), `5`, `10`, `15`, `30`, `60`,
  `1440`. `my_reminder_override`: the caller's own choice, absent for "Default" (the call's own). Nobody else's
  override is ever sent.
- `rsvps`: the caller's own answer for the whole series; `next_rsvps`: their answer for one occurrence only
  (`occurrence_start`). Keyed by the caller's user id, and never anyone else's: nobody sees who else answered what.
  `my_rsvp`/`my_rsvp_next_only`: the caller's own effective answer for `next_start` (or `start`), and whether it's a
  next-only one. Both absent until they answer.
- `can_cancel`: computed for the caller: the organiser, that channel's admins, that community's admins, system admins.
  Editing is the organiser's alone (`organizer_id` is the caller).
- `sequence`: the iTIP SEQUENCE. `cancelled`: the whole call (or series) was cancelled; the post stays.

## Routes

### Schedule a call — `POST /channels/{channel_id}/meetings`

Needs `create_post` in the channel (`403` otherwise; `400` for an invalid ID).

```json
{
  "summary": "Weekly sync",
  "description": "",
  "start": 1793541600000,
  "end": 1793543400000,
  "time_zone": "Europe/Paris",
  "recurrence_rule": "FREQ=WEEKLY;BYDAY=MO,TH",
  "reminder_minutes": 10
}
```

`summary` is required (at most 1000 characters; `description` 4000); the call lasts 1 minute to 24 hours;
`recurrence_rule` is `""` for a one-off call; `reminder_minutes` is one of the choices above (`400` otherwise).
Answers `200` with the meeting. Posts in the channel, sends `meeting_changed`, and emails iTIP invitations to the
members who asked for them.

### Read a call — `GET /meetings/{id}`

Answers `200` with the meeting. `404` when it doesn't exist or the caller can't read its channel (the same answer,
so a private channel's calls don't leak).

### Edit a call — `PUT /meetings/{id}`

The organiser only (`403` for anyone else, admins included; `404` as for reading). Same body as scheduling; answers
`200` with the meeting, updates the post, sends `meeting_changed` and iTIP updates. `409` once cancelled.

### Cancel — `DELETE /meetings/{id}` or `DELETE /meetings/{id}?scope=occurrence`

Whoever `can_cancel` (`403` otherwise; `404` as for reading). Without `scope` it cancels the call, or the whole
series; `scope=occurrence` cancels only the series' next occurrence (`next_start`; `409` when the series has none
left; on a one-off call it cancels the call). No body. Answers `200` with the meeting, and sends `meeting_changed`.

### Answer — `POST /meetings/{id}/rsvp`

Needs only to read the channel (`404` otherwise).

```json
{"status": "going", "scope": "next"}
```

`status` is `going`, `maybe` or `no`; `scope` is absent (or `""`) for the whole series, `"next"` for the next
occurrence only (`409` when there's none). `400` for anything else, `409` once cancelled. Answers `200` with the
meeting (`my_rsvp` updated), and sends `meeting_changed`.

### The caller's own reminder — `POST /meetings/{id}/reminder`

Needs only to read the channel (`404` otherwise).

```json
{"minutes": 60}
```

`minutes` is one of the reminder choices, or `null` for "Default" (`400` otherwise; `409` once cancelled). Answers
`200` with the meeting (`my_reminder_override` updated). Only the caller's own reminder changes: no email is sent.
Reminders are a direct message from the Calendar bot before each occurrence, to the channel's members who haven't
answered `no` for it.

### Add to calendar — `GET /meetings/{id}/ics`

Needs to read the channel (`404` otherwise). Answers `200` with the call's current iTIP `REQUEST` (or `CANCEL`) as
`text/calendar; method=...`, `Content-Disposition: attachment; filename="<id>.ics"`.

### The community's calls — `GET /meetings?team_id={team_id}&from={ms}&to={ms}`

The "Community calls" layer of the calendar (and the mobile app's Schedule). `400` for an invalid `team_id`, `from`
or `to` that isn't milliseconds, `from` after `to`, or a range over 100 days. `403` unless the caller is a member
of the community.

```json
{
  "occurrences": [
    {
      "meeting_id": "k3w1x9...",
      "post_id": "p7d4...",
      "channel_id": "c5f8...",
      "channel_name": "standup",
      "channel_display_name": "Standup",
      "channel_type": "O",
      "organizer_id": "u1b6...",
      "summary": "Weekly sync",
      "start": 1794146400000,
      "end": 1794148200000,
      "recurring": true,
      "my_rsvp": "going"
    }
  ],
  "truncated": false
}
```

One entry per occurrence overlapping `[from, to)`, by start time. Only from channels the caller may read in that
community: the channels they're in, its public channels (not for a guest who isn't in them), and private channels
only as a member; never archived channels, direct or group messages, or another community's channels. Cancelled
calls and cancelled occurrences are left out. `my_rsvp` is the caller's own answer for that occurrence (absent when
they haven't answered); nobody else's is sent. At most 500 occurrences, the earliest; `truncated` says more were
left out (ask for a shorter range). Open an occurrence's post with its permalink, `/<team name>/pl/<post_id>`.

### The channel's next call — `GET /channels/{channel_id}/meetings/next`

The channel header's chip. Needs `read_channel` (`404` otherwise); `400` in a direct or group message (or for an
invalid ID).

```json
{
  "next": {
    "meeting_id": "k3w1x9...",
    "post_id": "p7d4...",
    "summary": "Weekly sync",
    "start": 1794146400000,
    "end": 1794148200000,
    "live": false,
    "recurring": true
  }
}
```

The call live now (`live`: `start <= now < end`, by the server's clock), else the earliest next occurrence starting
within 7 days; cancelled calls and occurrences skipped. `{"next": null}` when there's none, and in an archived
channel. Clients flip the chip on their own clock from `start` and `end` ("in N min" in the last hour, "Live" from
`start`), and read this again when `end` passes (no sooner than a minute after the last read), on `meeting_changed`
for the channel, and on a channel switch; no faster polling.

### The caller's calendar feed — `GET /teams/{team_id}/feed` and `POST /teams/{team_id}/feed`

The "Subscribe…" dialog of the Community calls layer. `404` when the "Allow calendar feeds" app setting is off
(`calendar_feeds_enabled` in `GET /config` says so first), `400` for an invalid ID, `403` unless the caller is an
active member of the community. Both answer with `Cache-Control: no-store`.

`GET` says whether the caller has a feed of that community: `{"exists": true, "created_at": 1793000000000}`, or
`{"exists": false}`. Its address is never shown again: only the SHA-256 of its token is kept.

`POST` (no body) makes the caller a new feed of that community and revokes the one they had, at once ("Reset
link"); `403` for a bot or a deactivated account. It answers with the only copy of the address:

```json
{"url": "https://chat.example.com/plugins/com.antimatterchat.calendar/feeds/<token>.ics",
 "path": "/plugins/com.antimatterchat.calendar/feeds/<token>.ics",
 "created_at": 1793000000000}
```

`url` is absent when the server has no Site URL; build it from the page's origin and `path` then.

## The calendar feed — `GET /plugins/com.antimatterchat.calendar/feeds/{token}.ics`

The ONLY route of this app that works without a session: calendar apps read it on their own, and the token (32
random bytes, base64url) is its only credential. Anyone with the link sees what its member sees, so keep it private.
`HEAD` works too.

Every read checks everything again, from the token alone, as of now: the setting is on; the token is the member's
current one for that community (not reset); the member exists, isn't deactivated and isn't a bot; they're still an
active member of the community (not archived); and the calls are the ones `GET /meetings` would show them now (the
same read rule: only channels they may read in that community). Any failure, an unknown token included, answers the
same `404` with the same body (`Not found`, `text/plain`, `Cache-Control: no-store`), so none can be told apart.
Leaving the community or being deactivated also deletes the member's feeds, so rejoining doesn't bring the old link
back. A feed is read at most about 6 times a minute (a burst of 6, then one every 10 seconds, counted per server):
`429` with `Retry-After` (seconds) past that. The token is never logged.

`200` with `Content-Type: text/calendar; charset=utf-8`, `Cache-Control: private, max-age=900` and an `ETag` (a
hash of the body); `If-None-Match` with it answers `304`. The body is an iCalendar object (no `METHOD`):

- `X-WR-CALNAME` is the community's name; `REFRESH-INTERVAL;VALUE=DURATION:PT1H` and `X-PUBLISHED-TTL:PT1H`.
- One `VEVENT` per call or series with an occurrence from 30 days ago to a year ahead, at most 500 (the earliest
  kept). Cancelled calls are left out; a series' cancelled occurrences are `EXDATE`s.
- `UID` is the call's iTIP UID (as in the invitation emails and `GET /meetings/{id}/ics`), with its `SEQUENCE`;
  `DTSTAMP` is when the call last changed, so the body (and its `ETag`) changes only when the calls do.
- A series is written in its time zone (`DTSTART;TZID=...`, with a `VTIMEZONE`), a one-off call in UTC; `RRULE`
  as stored.
- `SUMMARY`; `DESCRIPTION` is the call's description, its channel's name and the post's permalink, which is also
  the `URL`.
- `ORGANIZER` is the organiser's address only when the server shows email addresses, else the server's
  notification address (as in the invitation emails).
- The one `ATTENDEE` is the member as `urn:uuid:<user id>` (no address, no name: a leaked link says nothing
  about whose it is), with their own `PARTSTAT` (for a series, their answer for the whole series);
  the `VALARM` is their own reminder (their override, else the call's). Nobody else's address, answer or reminder
  is ever in it.

## WebSocket: `custom_com.antimatterchat.calendar_meeting_changed`

Broadcast to the channel's members when one of its calls is scheduled, edited, cancelled (or one occurrence is), or
someone answers or changes their reminder:

```json
{"event": "custom_com.antimatterchat.calendar_meeting_changed", "data": {"id": "k3w1x9...", "channel_id": "c5f8..."}, "broadcast": {"channel_id": "c5f8..."}}
```

Read the meeting again (`GET /meetings/{id}`) if its post is on screen, the channel's next call
(`GET /channels/{channel_id}/meetings/next`) if that channel's header is, and the community's calls if they're shown.
The event carries no content: what the caller may see is always decided by those reads.
