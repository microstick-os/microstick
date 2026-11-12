# Device control and API

The core controls USB, Wi-Fi, mobile access, LAN and recovery. These functions
work without the web panel. The independent `web` and `statistics` plugins provide
HTTP access and traffic history. See the
[plugin contract](../buildroot-external/board/msm8916/overlay/usr/share/microstick/default-plugins/README.md)
for directory layout, environment, cleanup and lifecycle commands.

## Access and commands

Open `http://microstick.local/` or the device's USB/Wi-Fi address.
Local name discovery requires mDNS support on the client network.
To change the name, use the panel or this root command:

```sh
microstick api '{"method":"device","hostname":"travel-stick"}'
```

Names contain one lowercase ASCII DNS label, 1-63 characters, with internal
hyphens allowed. Status reports the actual name after mDNS conflicts.

Run device commands as root:

| Command | Action |
| --- | --- |
| `microstick status` | Read current state |
| `microstick api '{"method":"config"}'` | Read configuration without secrets |
| `microstick wifi ap`, `microstick wifi sta`, `microstick wifi off` | Select Wi-Fi operation |
| `microstick lte connect`, `microstick lte disconnect` | Enable or disable mobile access |
| `microstick clients [page]` | Read connected clients |
| `microstick lan show`, `microstick firewall show` | Read LAN or firewall configuration |
| `microstick lan apply FILE`, `microstick firewall apply FILE` | Apply a JSON configuration file |
| `microstick lan confirm TOKEN`, `microstick lan cancel` | Confirm or cancel a pending LAN change |
| `microstick ttl 64`, `microstick ttl off` | Set outgoing mobile IPv4 TTL or disable the rule |
| `microstick imei set IMEI`, `microstick imei restore` | Set an override or use the installation default; both reboot |
| `microstick usb rndis` | Select RNDIS for the next boot |

Replace `FILE`, `TOKEN` and `IMEI` with actual values. NCM requires `microstick usb ncm --experimental`.

## Core API

Version 1 uses `/run/microstick/control.sock`, a Unix `SOCK_SEQPACKET` socket.
Root and the dedicated web account can connect. Send one JSON object per request.
Read one JSON object per response. The maximum message size is 32767 bytes.
`microstick api` accepts the same request objects.

| Methods | Purpose |
| --- | --- |
| `status`, `config`, `operation` | Read state, configuration or operation result |
| `lan_config`, `firewall_config`, `clients` | Read LAN, firewall or client state; `clients.page` selects 32-row pages |
| `scan`, `connect`, `forget`, `wifi`, `mode`, `ap` | Change Wi-Fi or uplink policy |
| `lte`, `pin`, `ttl`, `imei`, `device`, `reboot` | Change mobile, identity, TTL or device settings |
| `lan`, `firewall`, `lan_confirm`, `lan_cancel` | Apply or confirm network configuration |
| `wifi_confirm` | Confirm a new Wi-Fi connection through its active STA address |

Write methods return `{"operation":"ID"}`. Poll `{"method":"operation"}` for
`id`, `busy` and `result`. Only one write runs at a time. Reads remain available.
`wifi_confirm` returns its result directly.

The web plugin maps read methods to `GET /api/<method>` and write methods to
`POST /api/<method>`. Use JSON bodies for POST requests.
Login uses `POST /api/login` with `password` and optional boolean `remember`.
Retain the session cookie. Send the returned token in `X-Microstick-CSRF` for
subsequent POST requests. POST requests also require a matching `Origin` and
`Content-Type: application/json`.

Remembered sessions last 30 days. The plugin stores token hashes in
`web/.state/sessions.json`. Logout revokes that session; a password change revokes
all sessions. Clock rollback before token creation blocks the token.

## Wi-Fi and mobile access

Auto mode selects AP plus mobile access when a SIM is present. Otherwise, it
selects saved Wi-Fi or AP. Saved mobile enable/disable settings survive reboot.
Scanning from AP briefly interrupts the hotspot.

A new Wi-Fi profile has 45 seconds for association and DHCP. After it receives an
address, open the panel through STA within 120 seconds. The panel confirms the
attempt automatically. USB access cannot confirm it. Internet checks must also
succeed before the profile is saved.

Failure restores AP and retains the previous profile. A recovery hold prevents
automatic retries across reboot. Start a new connection to retry deliberately.
Validated profiles reconnect without browser confirmation. Lost STA association
returns to AP after 30 seconds. Captive portal authentication is not implemented.

`connect` accepts optional `ssid_hex` for exact scanned SSID bytes. Manual names
use UTF-8. `status.wifi_connection` reports connectivity, `pending`, attempt `id`
and remaining time. Mobile labels use the actual radio technology.
Submit a SIM PIN once through `pin`; it is not saved or retried.
This image configures IPv4 only.

### Operator profiles

Use `lte.profile` to select `auto`, a catalog ID, or `manual`.
Automatic selection uses the SIM's home MCC/MNC. Specific SPN/GID1 matches take
priority over general network matches. Unknown or ambiguous SIMs require operator
selection. Missing metadata remains pending; the controller does not cycle APNs.

Requests that omit `profile` and supply a nonempty `apn` select manual mode.
Enable/disable-only requests keep the existing selection. Saved manual settings
survive updates.
Omitted manual credentials remain unchanged; an explicit empty value clears them.

The `lte` object in status/config includes `profile`, `profile_id`, `provider`,
`effective_apn` and `state`: `ready`, `pending`, `unknown` or `ambiguous`.
Config also supplies `lte.profiles`. Private credentials are excluded.

The build combines pinned provider-info 20221107 data with
[reviewed additions](../buildroot-external/board/msm8916/operator-profiles.json).
The result is `/usr/share/microstick/providers.json`. MMS and IMS entries are
excluded. Private and corporate APNs require manual settings.

## LAN and firewall

Store settings in root-private `/etc/microstick/lan.json` and `firewall.json`.
Use `lan` or `firewall` with `{"config":...}` to apply them.

| LAN field | Contract |
| --- | --- |
| `usb`, `ap` | Each has IPv4 CIDR `address`, DHCP `enabled`, pool `start`/`end`, `lease`, and `dns` |
| `lease` | 120-604800 seconds |
| `dns` | Zero to two IPv4 addresses advertised to clients |
| `reservations` | Up to 64 entries: `network`, unicast `mac`, `name` up to 64 UTF-8 bytes, and `ip` |

An empty reservation IP saves an alias only. Reserved addresses must be usable
in the selected LAN. LANs must not overlap each other or the active WAN.
The modem's own resolver uses WAN DNS.

Address, mask or DHCP-disable changes require confirmation within 120 seconds.
Call `lan_confirm` with `token`, or `lan_cancel`. Timeout or reboot restores the
committed configuration. Other DHCP changes commit after successful application.
Clients must renew DHCP to receive changed reservations.

| Firewall field | Contract |
| --- | --- |
| `rules` | Up to 64 forwarding-deny rules: `name`, `enabled`, `network`, `mac`, `destination`, `protocol`, `port` |
| `network` | `all`, `usb` or `ap`; MAC filters require a specific network |
| `destination` | Empty, IPv4 address or CIDR |
| `protocol` | `any`, `tcp`, `udp` or `both` |
| `port` | Empty, a port, or a range such as `8000-8010` |
| `services` | Up to 64 STA input permissions: `name`, `enabled`, `protocol`, required `port`, optional `source` |

Deny rules cover forwarded requests and replies, including established connections.
Device and destination filters must both match. Client MAC/IP observations are
not authentication.

Services use `tcp`, `udp` or `both`, with ports 1-65535. Source is an IP/CIDR;
an empty source selects the current STA subnet. A permission does not start a
program or create port forwarding. USB/AP access remains available. LTE input
is not opened. Local STA mDNS uses UDP5353.

## TTL and IMEI

TTL defaults to enabled with value 64. `/etc/microstick/ttl.conf` stores
`FIX_TTL_ENABLED=yes|no` and `FIX_TTL_VALUE=1..255`. The rule affects outgoing
local and forwarded IPv4 traffic on `wwan*`. Wi-Fi uplink traffic is unchanged.
Status/config return `ttl: {enabled,value,applied,error}`. Failed changes retain
the previous saved configuration and rules.

IMEI changes require 15 digits and a valid Luhn check digit. The override lives
in `/etc/microstick/imei.json`. `restore` removes the override and uses the
installation default. Status returns `current`, `configured`, `default`,
`effective`, `source` and `error` in `imei`.
Both IMEI commands reboot. The setting is reapplied before ModemManager starts.
`rmtfs -r -P -s` keeps modem NV changes in RAM; raw NV partitions are not rewritten.
A successful change does not establish operator acceptance or blacklist status.

## Statistics

The independent collector uses
`/run/microstick/plugins/statistics/runtime/api.sock`. It has the same transport,
message limit and root/web access as the core API. See the
[collector contract](../buildroot-external/board/msm8916/overlay/usr/share/microstick/default-plugins/statistics/README.md)
for local methods and stored data.

| Web endpoint | Parameters |
| --- | --- |
| `GET /api/statistics` | Current totals and state |
| `GET /api/statistics/series` | `source`: `lte` or `wifi` |
| `GET /api/statistics/history` | `period`: `day` or `month`; optional `source`: `lte` or `wifi` |
| `POST /api/statistics/reset` | Optional JSON `source`; default `lte` |
| `POST /api/statistics/config` | JSON `wifi_persistence` boolean |

The source name `lte` means all supported mobile traffic. Mobile history persists.
Wi-Fi session counters reset on disconnect, network change or reboot. Wi-Fi persistence is off
by default; enabling it counts subsequent traffic. Disabling it retains history.
Each saved source has 90 daily and 24 monthly buckets. Period reset retains the
archive. Totals are decimal strings. Counts include device and forwarded IP bytes;
operator billing can differ. Calendar history uses device localtime. Traffic
before clock synchronization remains undated.

