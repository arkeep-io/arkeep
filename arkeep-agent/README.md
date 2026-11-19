# Arkeep Agent

Back up Home Assistant with [Arkeep](https://arkeep.io), an open-source,
self-hosted backup manager built on [Restic](https://restic.net/) and
[Rclone](https://rclone.org/).

This app runs the Arkeep agent. It connects to your Arkeep server, which
schedules the backups and shows them in one dashboard, next to your other
machines. The agent can back up the Home Assistant backups, the configuration
and the `share` and `media` folders to any destination Arkeep supports: local
disk, S3-compatible storage, SFTP, a restic REST server or one of the Rclone
backends.

You need an Arkeep server that Home Assistant can reach. This app does not
include one: see the [Arkeep documentation](https://arkeep.io/docs/).
