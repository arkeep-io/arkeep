# Arkeep for Home Assistant

Home Assistant apps (called add-ons before Home Assistant 2026.2) published by
the [Arkeep](https://arkeep.io) project.

| App | Description |
|---|---|
| [Arkeep Agent](arkeep-agent/DOCS.md) | Back up Home Assistant with Restic and Rclone, managed from an Arkeep server |

## Install

Apps need Home Assistant OS: they are not available on Home Assistant
Container.

[![Add the Arkeep repository to Home Assistant](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Farkeep-io%2Farkeep%23home-assistant)

Or by hand:

1. In Home Assistant, go to **Settings** > **Apps** and select **Install app**.
2. In the top-right menu, select **Repositories**.
3. Add `https://github.com/arkeep-io/arkeep#home-assistant`.
4. Install **Arkeep Agent** from the list, then follow its **Documentation**
   tab.

## How this repository is published

The source is the `deploy/home-assistant` folder of the Arkeep repository.
Each stable Arkeep release copies it to the `home-assistant` branch with the
version of the release, once the agent image is published
(`scripts/publish-home-assistant-app.sh`, see ADR 0018 in `docs/adr`). Home
Assistant then offers the new version as an update.

Changes go through a pull request on `deploy/home-assistant`, never through a
commit on the `home-assistant` branch.

To try a change before a release, copy the `arkeep-agent` folder to the local
apps folder of a Home Assistant installation, which the Samba and SSH apps give
access to. The app then appears in the app store with the local apps.
