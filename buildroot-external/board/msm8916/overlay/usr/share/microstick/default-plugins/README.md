Put each plugin in `/data/plugins/<id>/`. Use only ASCII letters, digits, `_`,
or `-` in the directory name.

```text
example/
    run           Required executable entry point
    cleanup       Optional executable cleanup program
    disabled      Optional empty file that prevents startup
    .state/       Persistent data; created by the loader
    bin/          Your executables and other plugin files, if needed
```

Make the plugin directory, `run`, and optional `cleanup` owned by root.
Do not use symbolic links for these paths.
Do not allow group or other write access to the directory or its immediate children.
Make `run` executable with `chmod 755 run`. Apply the same permission to `cleanup`, if present.

The loader starts `run` from the plugin directory. Keep the program in the
foreground. Example `run`:

```sh
#!/bin/sh
exec ./bin/my-program
```

The supervisor restarts the program if it exits. Handle termination signals
in the main process.

The loader sets these variables for `run`:

| Variable | Value |
| --- | --- |
| `MICROSTICK_PLUGIN_DIR` | `/data/plugins/<id>` |
| `MICROSTICK_PLUGIN_DATA` | `/data/plugins/<id>/.state` |
| `MICROSTICK_PLUGIN_RUNTIME` | `/run/microstick/plugins/<id>/runtime` |
| `MICROSTICK_CORE_SOCKET` | `/run/microstick/control.sock` |
| `MICROSTICK_CORE_API` | `1` |

Store persistent data in `$MICROSTICK_PLUGIN_DATA`. Store temporary files in
`$MICROSTICK_PLUGIN_RUNTIME`. The loader creates both directories before startup.
Deleting the plugin directory also deletes its persistent data. A reboot clears
the runtime directory.

The loader saves a copy of `cleanup` in RAM before startup. It calls this copy
after stopping a plugin through `stop`, removal, or disabling. An ordinary
process exit does not call `cleanup`.

Make `cleanup` safe to run repeatedly. It must work without the original plugin
directory. Remove only resources that belong to your plugin. Do not create the
deleted plugin directory again.

Cleanup receives the three `MICROSTICK_PLUGIN_*` variables listed above.

## Control

Run these commands as root on the device. Replace `example` with your plugin ID.

```sh
microstick plugins list            # Show plugin status
microstick plugins rescan          # Load added plugins and apply directory changes
microstick plugins stop example    # Stop temporarily and call cleanup
microstick plugins start example   # Start a stopped plugin
```

To disable a plugin across reboots, create an empty `disabled` file in its directory.
To enable it again, remove this file. Then run `microstick plugins rescan`.

To remove a plugin, delete its directory. Save any required data first.
Deleted default plugins do not return automatically.
