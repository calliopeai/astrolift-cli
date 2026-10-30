# Security Policy

## Reporting a Vulnerability

This repository is public. Do not open a public issue for a vulnerability;
an issue is visible to everyone the moment it is filed. Report through GitHub
private vulnerability reporting on this repository: the Security tab, then
"Report a vulnerability". Only the maintainers see the report.

Include:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

We aim to acknowledge a report within 48 hours, and to release a fix within
7 days for critical issues.

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
- Rotate deploy tokens regularly from the dashboard (the app's Tokens page or
  Administration > Tokens); `astro ci setup <app>` pushes the current deploy
  token to the repository's CI secrets.
- Use `astro auth status` to check token expiry; let `astro auth refresh`
  rotate access tokens rather than long-lived credentials.
- Verify the platform you're logging into — `astro server add <slug> <api-url>`
  binds a slug to a URL; check the URL before completing the device flow.
- Keep the CLI current with `astro update`. `astro version-check` confirms
  the selected server serves the install discovery this CLI relies on.
