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
  a reminder, location, description. An occurrence of a repeating event is edited or deleted
  alone (an override with a `RECURRENCE-ID`, or an `EXDATE`), with the following ones (the series
  ends before it and a new one starts with it), or with the whole series. Edits are conditional
  on the event's ETag, so changes made elsewhere aren't overwritten.
- **Meet in Antimatter.** An event can be linked to a channel, a voice channel or a call: its row
  then has a Join (voice channel), Start call / Join call or Open button, which goes through the
  Voice channels and Calls plugins when they're installed. The link is stored in the event as
  `X-ANTIMATTER-CHANNEL-ID` / `X-ANTIMATTER-LINK` properties, with the channel's URL for other
  calendar clients.
- **Reminders.** The Calendar bot sends a direct message before events, at the time of their
  alarms or, for events without alarms, the time each user chooses (10 minutes by default). A
  cluster job checks every minute and reads each user's next day of events every 10 minutes.
  Reminders are written in the user's language and time zone: English, French, German and
  Spanish for now.

## Translations

The web app's texts are `react-intl` messages with English defaults. The server's texts (the
reminders) are [go-i18n](https://github.com/nicksnyder/go-i18n) messages with English defaults in
the code, translated in `assets/i18n/active.<locale>.json`, which the plugin bundle ships. To add
a language, copy `active.fr.json` to the new locale (as Antimatter names it, e.g. `pt-BR`) and
translate its values, keeping the `{{.Placeholders}}` and the `one`/`other` plural forms.

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

Turn on both connection settings above, set up a user as the image's documentation explains, then
connect with server `http://localhost:5232` (or the Docker host's address, as seen from the
Antimatter server).

## Development

```sh
make dist      # build the plugin bundle in dist/
make test      # run the server and webapp tests
make check-style
```

Set `MM_SERVICESETTINGS_ENABLEDEVELOPER=true` to only build the server for the current platform.

## Documentation

See the [user guide](https://antimatter.chat/docs/users/apps.html#calendar) and the
[admin guide](https://antimatter.chat/docs/admins/apps.html#mail-calendar) on
[antimatter.chat/docs](https://antimatter.chat/docs/).

## Releases

Pushing a `v*` tag builds the app and publishes it as a
[GitHub release](https://github.com/AntimatterChat/antimatter-plugin-calendar/releases) ([release workflow](.github/workflows/release.yml),
using the organisation's shared
[app release workflow](https://github.com/AntimatterChat/.github/blob/main/.github/workflows/app-release.yml)).
The version is the tag without its `v`. Tags with a `-`, such as `v1.2.0-rc.1`, are published as prereleases. Each release has:

- `com.antimatterchat.calendar-<version>.tar.gz`: the app bundle, with the server for Linux (amd64, arm64), macOS (amd64, arm64) and Windows (amd64)
- `com.antimatterchat.calendar-<version>.tar.gz.sig`: its detached signature with the Antimatter plugin signing key
- `SHA256SUMS`: the checksums of the release files

Install the bundle with **Upload Plugin** in **System Console > Plugins > Plugin Management** or
`amctl plugin install-url <bundle URL>` (both need `AM_PLUGINSETTINGS_ENABLEUPLOADS=true`), see
[Installing other apps](https://antimatter.chat/docs/admins/apps.html). A release without `.sig`
files is unsigned (its notes say so): a server with Require Plugin Signature
(`PluginSettings.RequirePluginSignature`) on refuses it. Running the workflow by hand (*Actions* → *Release* → *Run workflow*)
builds the bundle as a workflow artifact, and as a draft prerelease if you tick the box; builds
of untagged commits are versioned after the latest tag and the commit.

## Feedback and support

Found a bug in the Calendar plugin, or missing a feature? Please [open an
issue](https://github.com/AntimatterChat/community/issues/new/choose) in the [Antimatter community
repository](https://github.com/AntimatterChat/community), where the feedback for every part of
Antimatter is gathered. Questions and ideas are welcome in its
[Discussions](https://github.com/AntimatterChat/community/discussions). Please report security
problems privately, as explained in the [security
policy](https://github.com/AntimatterChat/.github/blob/main/SECURITY.md).

## License

GNU Affero General Public License v3.0, see [LICENSE.txt](LICENSE.txt). The build tooling is derived
from Apache-2.0 licensed Mattermost plugins, see [NOTICE.txt](NOTICE.txt).
