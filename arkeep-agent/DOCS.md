# Arkeep Agent

This app runs the [Arkeep](https://arkeep.io) agent on Home Assistant. The
agent connects to your Arkeep server and runs the backups and restores the
server asks for. Everything else happens in the Arkeep web interface:
destinations, schedules, retention, notifications and restores.

## Before you start

You need a running Arkeep server that Home Assistant can reach, and two values
from it:

- the address of its gRPC endpoint, `host:port` (port 9090 by default);
- its agent secret, the `ARKEEP_AGENT_SECRET` set on the server.

The agent only makes outbound connections to the server. It needs no open port
on Home Assistant.

## Setup

1. Install the app.
2. In the **Configuration** tab, fill in **Server gRPC address** and **Agent
   secret**. If the server is behind a reverse proxy, also set **Server HTTP
   URL** (see below).
3. Start the app and open the **Log** tab. The agent reports
   `enrollment successful`, then `registered with server`.
4. In Arkeep, the agent appears under **Agents**, named after the **Hostname**
   option (`homeassistant` by default).

## Options

| Option | Required | Description |
|---|---|---|
| Server gRPC address | yes | `host:port` of the server's gRPC endpoint, without `https://`. Example: `arkeep.example.com:9090`. |
| Agent secret | yes | The server's `ARKEEP_AGENT_SECRET`. |
| Server HTTP URL | no | URL of the server's HTTP API, used once to enroll the agent and obtain its certificate. By default the agent uses the gRPC host on port 8080 over plain HTTP. Set it when the server is behind a reverse proxy, for example `https://arkeep.example.com`. |
| Hostname | yes | Name of the agent in Arkeep, also recorded on every snapshot. Keep it stable: Arkeep groups snapshots by hostname. With several Home Assistant installations, give each one its own. |
| CA certificate | no | Path of a CA certificate, under `/ssl`, when the server uses a gRPC certificate from a private CA. Not needed with the certificate Arkeep generates itself. |
| Log level | yes | `debug`, `info`, `warn` or `error`. |

Enrolling over plain HTTP does not expose the agent secret: the agent proves
it knows the secret without sending it, and accepts the server's answer only
with the server's proof. The server must be at least as recent as the app.

Restart the app after changing an option.

## What the agent can back up

Use these paths as sources in an Arkeep policy, and as targets of a restore:

| Path | Content | Access |
|---|---|---|
| `/backup` | Home Assistant backups | read and write |
| `/share` | The `share` folder | read and write |
| `/media` | The `media` folder | read and write |
| `/homeassistant` | Home Assistant configuration | read only |
| `/app_configs` | Configuration folders of the installed apps | read only |
| `/ssl` | Certificates | read only |

Network storage added to Home Assistant with the usage **Share** or **Media**
appears under `/share/<name>` or `/media/<name>`.

### Recommended policy

Home Assistant backups are the supported way to restore a system: they contain
the configuration, the database and the data of every app, in a consistent
state. Let Home Assistant create them, and let Arkeep keep copies elsewhere:

1. In Home Assistant, turn on automatic backups (**Settings** > **System** >
   **Backups**).
2. In Arkeep, create a policy for this agent with `/backup` as its source,
   scheduled after the Home Assistant backup, to a destination outside this
   machine.

Backing up `/homeassistant` directly gives a file-level history of the
configuration. The database files in it change while Home Assistant runs, so
do not rely on that copy of them.

A local destination must be under `/share` or `/media`, and outside the folders
the policy backs up.

## Restoring

To get a lost Home Assistant backup back, restore it from Arkeep to its
**Original location**. The file is then back in the Home Assistant backup
folder, where Home Assistant can restore it.

`/homeassistant`, `/app_configs` and `/ssl` are read only. To restore files
from them, restore to a folder under `/share` and copy them from there.

On a new Home Assistant installation, install this app again and give it the
same **Hostname**. It enrolls as a new agent, which can restore the snapshots
of the previous one from the same destination. The previous agent stays in
Arkeep, offline, until you delete it.

## What is different from other installations

- No Docker volumes: the app has no access to Docker, so Docker volume sources
  and hooks that run `docker` commands do not work here.
- Paths are the ones listed above. The app does not see the rest of the host.
- Hooks and command sources configured in Arkeep run inside the app, as root,
  with the access listed above.

## App data

The app keeps the agent's certificate and state in its own data folder, which
Home Assistant backups include. The Restic cache and the Restic and Rclone
programs are left out of them: the agent rebuilds them.

Without this data, for instance after uninstalling the app, the agent enrolls
again as a new agent.

## Support

- Documentation: <https://arkeep.io/docs/>
- Questions: <https://github.com/arkeep-io/arkeep/discussions>
- Bugs: <https://github.com/arkeep-io/arkeep/issues>

Arkeep is released under the Apache-2.0 license.
