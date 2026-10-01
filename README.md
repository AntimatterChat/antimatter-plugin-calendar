# Calendar

An Antimatter plugin to see and manage your calendar without leaving Antimatter. Each user connects
their own calendar server over CalDAV, and gets reminders from the Calendar bot before events.

## Features

- **Calendar in the app bar.** Calendar opens its panel in the right-hand slot. Under the Fusion web
  UI the panel uses Fusion's own styles, as in its mockup; the classic web app gets a
  classic-styled version.
- **Your own calendar server.** Each user connects a CalDAV server (Nextcloud, Fastmail, iCloud,
  Radicale, Baïkal, SOGo...) from the panel: its CalDAV URL or just its domain, found through
  `/.well-known/caldav` or DNS. The password is stored encrypted (AES-GCM, with a key the plugin
  keeps in its KV store) and never logged. Use an app password where your provider offers them.
- **Agenda and week views** of all the user's calendars, in their colors. Repeating events are
  expanded (with their exceptions and moved occurrences), across daylight saving time changes.
- **Create, edit and delete events**: title, calendar, times or all day, a simple repeat rule,
  a reminder, location, description. Repeating events are edited as a whole; one occurrence can be
  deleted. Edits are conditional on the event's ETag, so changes made elsewhere aren't overwritten.
- **Meet in Antimatter.** An event can be linked to a channel, a voice channel or a call: its row
  then has a Join (voice channel), Start call / Join call or Open button, which goes through the
  Voice channels and Calls plugins when they're installed. The link is stored in the event as
  `X-ANTIMATTER-CHANNEL-ID` / `X-ANTIMATTER-LINK` properties, with the channel's URL for other
  calendar clients.
- **Reminders.** The Calendar bot sends a direct message before events, at the time of their
  alarms or, for events without alarms, the time each user chooses (10 minutes by default). A
  cluster job checks every minute and reads each user's next day of events every 10 minutes.

## Configuration

In **System Console > Plugins > Calendar**:

- **Send reminders** (on).
- **Allow calendar servers on private networks** (off): users can only connect to public addresses
  unless this is on, so they can't reach the internal services of the network the server runs in.
  Turn it on for a calendar server on your internal network.
- **Allow unencrypted connections** (off): calendar servers must use HTTPS unless this is on.

## Trying it out

Any CalDAV account works, e.g. Fastmail (`caldav.fastmail.com`), iCloud (`caldav.icloud.com`,
with an app-specific password) or a Nextcloud server (`https://cloud.example.com/remote.php/dav`).

For a local test server, [Radicale](https://radicale.org) runs in one command:

```sh
docker run --rm -p 5232:5232 tomsquest/docker-radicale
```

Turn on both connection settings above, open `http://localhost:5232` once to create a user (any
username and password with the default configuration), then connect with server
`http://localhost:5232`.

## Development

```sh
make dist      # build the plugin bundle in dist/
make test      # run the server and webapp tests
make check-style
```

Set `MM_SERVICESETTINGS_ENABLEDEVELOPER=true` to only build the server for the current platform.

## License

GNU Affero General Public License v3.0, see [LICENSE.txt](LICENSE.txt). The build tooling is derived
from Apache-2.0 licensed Mattermost plugins, see [NOTICE.txt](NOTICE.txt).
