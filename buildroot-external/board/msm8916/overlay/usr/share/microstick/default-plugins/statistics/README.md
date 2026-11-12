# Statistics plugin

This plugin records mobile and Wi-Fi IP traffic. Counts include device and
forwarded traffic. Operator billing can differ.

## API v1

Run a request as root:

```sh
/data/plugins/statistics/bin/microstick-statistics --request '{"method":"status"}'
```

The socket is `$MICROSTICK_PLUGIN_RUNTIME/api.sock`, using Unix `SOCK_SEQPACKET`.
Send one JSON object per request. Read one JSON object per response.
Limit each message to 32767 bytes. Access is limited to root and the web account.
The collector has no HTTP listener.

Use `lte` for mobile traffic or `wifi` for Wi-Fi traffic in `source`.

| Method | Parameters and result |
| --- | --- |
| `status` | Mobile totals, Wi-Fi session, `wifi.persistence` and `wifi.saved` |
| `series` | Required `source`; returns recent speed samples |
| `history` | Required `period`: `day` or `month`; optional `source`, default `lte` |
| `reset_period` | Optional `source`, default `lte`; resets period totals and retains history |
| `configure` | Required boolean `wifi_persistence`; default `false` |

Byte totals are decimal strings. Mobile and Wi-Fi totals remain separate.

## Stored data

Files are in `$MICROSTICK_PLUGIN_DATA`, normally `statistics/.state/`:

| Files | Contents |
| --- | --- |
| `lte-0.bin`, `lte-1.bin` | Mobile snapshots |
| `wifi-0.bin`, `wifi-1.bin` | Wi-Fi snapshots, when persistence is enabled |
| `settings.json` | Wi-Fi persistence setting |

Each source retains 90 daily and 24 monthly history entries.
Enabling Wi-Fi persistence counts subsequent traffic. Disabling it pauses
recording and retains history. Wi-Fi session counters and speed samples stay in RAM.

Changed data is saved after 300 seconds or 64 MiB, with at least 60 seconds between automatic saves.
Period reset and normal stop save immediately. Calendar history uses device
localtime. Traffic recorded before clock synchronization remains undated.
Invalid Wi-Fi snapshots are preserved; mobile collection continues.

## Control and cleanup

```sh
microstick plugins stop statistics
microstick plugins start statistics
```

Stop calls `cleanup` to remove the plugin's private counter table.
Before a userspace update from counter version 1, stop the plugin to run cleanup.
Start it after the update. Reboot is an alternative. Do not delete persistent data.

See the [plugin contract](../README.md) for installation, disabling and removal.
