# Security Policy

## Reporting a Vulnerability

**This repository is private.** Everyone who can read this file already has
repository access, so the reliable channel today is a new issue in this
repository, titled with a `[security]` prefix. While the repository is
private that issue is not publicly visible.

Include:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

We aim to acknowledge a report within 48 hours, and to release a fix within
7 days for critical issues.

### Before this repository becomes public

A repository issue stops being confidential the moment the repository is
public. Ahead of that, this policy has to move to GitHub private
vulnerability reporting, which is only available on public repositories.
Tracked in calliopeai/astrolift-app#1404.

Earlier revisions of this policy directed reports to an address on a domain
with no nameserver delegation and no MX record, so mail to it could never be
delivered and reports were lost silently. Do not reintroduce an email contact
here without first confirming the domain resolves and accepts mail.

## Supported Versions

| Version | Supported |
| ------- | --------- |
| latest  | Yes       |

The CLI is pre-1.0 and surface may shift; only the latest tagged release
is supported. When 1.0 ships, this matrix will expand.

## Security Best Practices

When using the Astrolift CLI:

- Credentials are stored at `~/.config/astrolift/credentials/<server>.yaml`
  with mode `0600`. The CLI refuses to read credentials files with wider
  permissions — don't loosen them.
- Use a deploy token (`ASTROLIFT_DEPLOY_TOKEN`) for CI; never use a
  personal access token from automation.
- Don't commit `~/.config/astrolift/` or any file under it to source
  control.
- Rotate deploy tokens regularly via `astro app tokens` (per-app) or
  `astro org tokens` (org-scoped).
- Use `astro auth status` to check token expiry; let `astro auth refresh`
  rotate access tokens rather than long-lived credentials.
- Verify the platform you're logging into — `astro server add <slug> <api-url>`
  binds a slug to a URL; check the URL before completing the device flow.
- Run `astro version-check` periodically; the platform reports the
  minimum supported CLI and the upgrade URL.
