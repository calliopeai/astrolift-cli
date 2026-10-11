# Set up and test install alert SMTP

This tests the install's configured Django SMTP alert channel with one fixed message
to your current registered mailbox. It is separate from [app-owned SES tests](email-delivery.md).
The default `django_ses.SESBackend`, Azure ACS, app SMTP and plaintext SMTP are unsupported
by this diagnostic. There is no transport fallback or relay configuration mutation.

The matching offline topic is `astro docs show install-alert-mail` (aliases
`alert-mail` and `install-smtp`). Older CLI releases may not include this guide.
Use the current server schema and `installAlertMailSupport`; the SES capability
keys do not advertise support for this install channel.

## 1. Configure the install channel privately

An install operator configures these existing settings through the install's
private configuration and normal configuration rollout:

| Setting | Required value or purpose |
|---|---|
| `DJANGO_EMAIL_BACKEND` | `django.core.mail.backends.smtp.EmailBackend` |
| `EMAIL_HOST`, `EMAIL_PORT` | The configured relay hostname and port |
| `FROM_EMAIL` | Configured diagnostic sender |
| `EMAIL_HOST_USER`, `EMAIL_HOST_PASSWORD` | Relay login, if required; keep private |
| `EMAIL_USE_TLS` | `true` for STARTTLS, with `EMAIL_USE_SSL=false` |
| `EMAIL_USE_SSL` | `true` for implicit TLS, with `EMAIL_USE_TLS=false` |
| `EMAIL_SSL_KEYFILE`, `EMAIL_SSL_CERTFILE` | Optional private client-certificate paths |

Exactly one TLS mode must be enabled. The native attempt verifies the relay's TLS
certificate and hostname. The support query describes configured TLS, not an observed
successful connection. Do not put relay credentials or client keys in app manifests,
GraphQL variables, shell history, screenshots or the documentation repository.
Changing these settings does not configure app-owned mail services.

The install's `EMAIL_NOTIFICATIONS` master flag must be enabled through authorized
install configuration administration. It defaults off. The caller's current email
preference for the selected event must also be enabled. This diagnostic does not
verify scheduler execution, notification fanout or ordinary alert delivery.

## 2. Check your event preference

These eight existing email event kinds default enabled once the master flag is on;
an explicit caller preference can mute each one independently:

| Event kind | Meaning |
|---|---|
| `deploy.failed` | Deployment failed; diagnostic default |
| `app.down` | App unavailable |
| `app.recovered` | App recovered |
| `domain.cert_expiring` | Certificate expiring |
| `domain.cert_renewal_failed` | Certificate renewal failed |
| `cluster.bootstrap_failed` | Cluster bootstrap failed |
| `secret.revealed` | Secret revealed |
| `app.deregister_pending` | App deregistration pending |

The existing self-only preference API reads and sets the caller's toggle; it does
not change another user's preference or grant operator authority:

```graphql
query OwnEmailPreferences {
  astroliftMyNotificationPreferences(channel: "email") { channel eventKind enabled }
}
```

To intentionally change your own toggle, save this as `alert-preference.graphql`:

```graphql
mutation OwnEmailPreference($input: SetNotificationPreferenceInput!) {
  setNotificationPreference(input: $input) {
    ok errors { code message }
    data { channel eventKind enabled }
  }
}
```

With `umask 077`, save a private `alert-preference.json`:

```json
{"input":{"channel":"email","eventKind":"deploy.failed","enabled":true}}
```

```bash
astro --server staging --org ORGANIZATION_GUID api graphql \
  --file alert-preference.graphql --vars-file alert-preference.json
```

Inspect `ok/errors/data`; setting a preference does not send the diagnostic.

## 3. Review the current source in Notifications

Select the current organization and open **Settings → Notifications → Install alert
email test** (`/settings/notifications?section=install-email`). The **Test platform
alert email** link on the domain Email delivery page opens the same section.
Navigation uses the advisory `org.update` hint. The server separately requires an
active platform operator, current organization membership and `org.update` for
support, send and history. An ordinary organization administrator is not admitted
merely because a link is visible. Bearer credentials need the current admin scope
and organization ceiling; session/credential withdrawal is rechecked during transport.

Choose the event, review the sender, your own registered mailbox, configured TLS
mode and check time, then select **Review source and mailbox**. There is no editable
recipient, subject, body, relay or credential. Select **Send reviewed alert test**
only for an intentional test. The page rechecks the source immediately before dispatch.

For CLI/API use, save `alert-support.graphql`:

```graphql
query InstallAlertMailSupport($event: String!) {
  installAlertMailSupport(eventKind: $event) {
    allowed reason transport sender recipient tlsMode sourceFingerprint checkedAt
  }
}
```

```bash
astro --server staging --org ORGANIZATION_GUID api graphql \
  --file alert-support.graphql --var event=deploy.failed
```

A denied query or `allowed=false` is a refusal, not an empty healthy configuration.
The opaque `sourceFingerprint` binds current settings and the selected event using
the install's private HMAC facility. It is not an SMTP account/incarnation proof.
Client-certificate paths are settings metadata, not a hash of certificate contents.

## 4. Send one reviewed intent

Save `alert-send.graphql`:

```graphql
mutation InstallAlertMailTest($input: SendInstallAlertMailTestInput!) {
  sendInstallAlertMailTest(input: $input) {
    ok errors { code message }
    data {
      id requestId version eventKind transport sender recipient status reasonCode
      createdAt acceptedAt deliveryObserved
    }
  }
}
```

Generate one UUID for the intentionally reviewed test and retain it. With `umask 077`,
save a private `alert-test.json`, replacing the placeholders with that original UUID
and the exact current reviewed fingerprint:

```json
{
  "input": {
    "requestId": "ORIGINAL_REQUEST_UUID",
    "expectedSourceFingerprint": "REVIEWED_SOURCE_FINGERPRINT",
    "eventKind": "deploy.failed"
  }
}
```

```bash
astro --server staging --org ORGANIZATION_GUID api graphql \
  --file alert-send.graphql --vars-file alert-test.json
```

The send service refuses an enclosing database transaction or disabled autocommit
with `ALERT_MAIL_ENCLOSING_TRANSACTION_UNSUPPORTED`, after current permission
admission and before reserving an intent or contacting SMTP. Intent and pre-DATA
transitions must commit independently before native effects. Internal integrations
must not wrap the service in a transaction that could roll back nonce history
after sending.

There is no dedicated test-send CLI verb. HTTP success or CLI exit zero does not
establish mutation success; inspect GraphQL errors, `ok/errors/data` and the receipt.
The message content is fixed. The receipt stores addresses, event and intent/source
metadata, not relay credentials, subject/body or raw native replies. The UI stores
only actor/organization/event-bound nonce/fingerprint recovery metadata in session storage.

## 5. Recover without resending

| Status | Interpretation |
|---|---|
| `reserved` | Intent committed; the attempt may be in progress |
| `sent` | Pre-DATA intent committed; acceptance is unconfirmed |
| `accepted` | Original final SMTP DATA acknowledgement observed and privately recorded |
| `failed` | Known failure; fixed reason available |
| `unknown` | Final acceptance unconfirmed; never assume no message was accepted |

Every receipt has `deliveryObserved=false`. SMTP acceptance transfers responsibility
to the relay; it does not prove delivery, inbox placement or current transport health.
A source/authority change after acceptance preserves the private acknowledgement
and may refuse the public response. Replaying the same original nonce under current
original-source admission returns its record and never reconnects or resends.

After a lost reply, use **Refresh checks and history**, retaining the request UUID.
The UI offers a new intentional test only after a correlated accepted or known-failed
receipt. `reserved`, `sent`, `unknown`, invalid completion and failed history reads
remain recovery-only. Do not automatically create a fresh UUID after ambiguity.

Save `alert-history.graphql` for current own-caller recovery:

```graphql
query InstallAlertMailHistory($event: String!, $after: String, $limit: Int!) {
  installAlertMailTestsPage(eventKind: $event, after: $after, limit: $limit) {
    items { id requestId version status reasonCode acceptedAt deliveryObserved }
    totalCount nextCursor
  }
}
```

```bash
astro --server staging --org ORGANIZATION_GUID api graphql \
  --file alert-history.graphql --var event=deploy.failed --var limit=25
```

Page using `nextCursor`; the maximum is 50, with cursor admission bound to the original
actor, organization and event. History remains readable to an admitted original
operator after source rotation; the send replay refuses a changed source.

New tests are limited to three per caller and ten per organization per minute. There
is one transport attempt. Socket operations are capped at five seconds with a
15-second active SMTP budget. System DNS and PostgreSQL are external synchronous
services, so this is not a hard end-to-end deadline. Unknown outcomes stay unknown.
Retained receipt history prevents migration rollback; use a forward fix, not history deletion.

See [capabilities](capabilities.md) and the [API reference](../reference/api.md).
The whole email transport and alert-fanout scope remains open (#2289).
