# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in the Astrolift CLI, please report
it responsibly.

**Do not open a public issue.**

Instead, email **security@astrolift.app** with:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

We will acknowledge your report within 48 hours and aim to release a fix
within 7 days for critical issues.

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
