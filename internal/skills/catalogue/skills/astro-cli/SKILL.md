---
name: astro-cli
description: Install and use the astro CLI — authenticate via browser device flow, check platform status, validate manifests, drive CI deploys, and diagnose permissions from the terminal.
version: "0.1.0"
---

# The `astro` CLI

## Install

Download from the platform's **Downloads** page (platform UI sidebar), put it on PATH. `astro update` self-updates later; `astro version-check` confirms compatibility with the platform.

## Connect + authenticate (one-time)

```bash
astro server add prod https://<your-astrolift-host>
astro server use prod
astro auth login          # browser device-flow login against the install's SSO
astro auth status         # confirm; `astro auth refresh` when the token ages out
```

## Useful commands

```bash
astro status                   # platform health at a glance
astro perms diagnose           # what you can do, and why something 403'd
astro scm list                 # webhooks wired for repos
astro ci render                # validate astrolift.toml locally — see the
                               # platform-side manifests BEFORE merging
astro ci status                # state of a deploy workflow
astro docs                     # open platform docs
```

`astro ci deploy` is for CI runners (deploy-token driven) — apps using the managed workflow already deploy on merge, so you rarely call it by hand.

## Honest note on coverage

The CLI is young: some `astro app <...>` subcommands are still being wired to the API — if one answers "structured but not yet wired", do that operation in the web UI (app page) instead. Auth, server, status, perms, scm, and the ci group are solid.
