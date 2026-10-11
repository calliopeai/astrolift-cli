# Test an exact email service

Use this flow to request one diagnostic message through an application's applied
AWS SES managed service, then inspect its content-free history. It does not test
the installation's global alert channel or certify general mail delivery.

The release-matched offline guide is `astro docs show email-delivery`, with
`mail` and `email` aliases. The operations below use the existing
`astro api graphql` command; there is no dedicated test-send CLI verb.

## Check support and authority

Run `astro status --json` and check for `email.exact_service_delivery_tests`,
`email.delivery_test_history` and `email.signed_delivery_observations`. These
markers describe the installed API. They grant no permission and do not prove
that SES, DNS, feedback delivery or an individual service is ready.

Select the exact organization and managed-service GUID. The service must be
active, have no unfinished operation, and retain an applied `email` / `ses`
binding to its current app and environment. The server derives the original
account, region, SES identity and sender from that binding and its current cluster
credential declaration; callers cannot supply another provider or sender.

Sending and the support check require fresh `app.update` and
`managed_service.update` authority on the exact service/app, including current
membership, credential ceilings and policy checks. A bearer currently needs the
`admin` scope ceiling for `managed_service.update`; `write:apps` alone is
insufficient. This guide does not widen scopes or replace organization grants.
History reads require current `app.read` authority. Account permission lists are
informational; they do not establish current bearer or target authority.

Save this document as `email-support.graphql`:

```graphql
query EmailTestSupport($service: GUID!) {
  emailDeliveryTestSupport(managedServiceId: $service) {
    allowed reason serviceVersion sender identity accountId region
  }
}
```

```bash
astro --server staging --org ORGANIZATION_GUID api graphql \
  --file email-support.graphql --var service=MANAGED_SERVICE_GUID
```

An allowed support response confirms the current applied-source metadata and
write admission. It performs no native sending-identity, suppression or
configuration-set check. Review its exact service version, sender, account,
region and identity before preparing a message.

## Use the domain or app entry

Open **Domains**, select the exact registered domain, then **Domain email
delivery** (`/domains/DOMAIN_GUID/email`). Choose a currently authorized app and
its exact email service/environment; search and page through the available
choices rather than inferring a sender from the app's routed hostname. The
current-source panel shows the service version, sender, identity, AWS account
and region. The association notice compares the sender with the exact domain
or a dot-separated subdomain; this name match proves neither DNS authentication
nor permission to use another service.

Enter one agreed recipient and, optionally, a subject and message. Select
**Review recipient and source**, then **Send reviewed test**. Editing the content
or refreshing the current source requires another review. Read the returned
outcome and **Delivery test history**; an accepted result is not delivery.
The app's managed-service summary and email-service detail also use this same
exact-service review and history flow.

**Mail DNS checks** runs separate bounded MX, apex TXT/SPF and DMARC TXT
lookups. Supply the operator's explicit DKIM selector and choose **DKIM record
type** `TXT` or `CNAME` to match the provider's declared setup. Do not guess the
selector from an app name. The result identifies the hostname, record type,
observation perspective and time. It neither changes DNS nor sends email.

On an uncertain reply, refresh history for the original request ID before
another send. Reload recovery retains content-free request metadata, not the
recipient, subject or message draft. Re-enter the original content to retry
that exact request. A changed source or lost current authority invalidates the
original review; SMTP, Azure email and alert-channel tests have no fallback here.

## Inspect email DNS separately

Use the exact registered domain GUID from the selected installation. Replace
`example.com` with that zone, and `DKIM_SELECTOR` with the selector explicitly
provided by your email operator or sending provider. Do not guess a selector
from the app, sender or service name.

```sh
astro --server staging --org ORGANIZATION_GUID operator domains lookup DOMAIN_GUID \
  --hostname example.com --record-type MX --json
astro --server staging --org ORGANIZATION_GUID operator domains lookup DOMAIN_GUID \
  --hostname example.com --record-type TXT --json
astro --server staging --org ORGANIZATION_GUID operator domains lookup DOMAIN_GUID \
  --hostname _dmarc.example.com --record-type TXT --json
astro --server staging --org ORGANIZATION_GUID operator domains lookup DOMAIN_GUID \
  --hostname DKIM_SELECTOR._domainkey.example.com --record-type TXT --json
```

The apex TXT answer can contain SPF (`v=spf1`); the `_dmarc` TXT answer can contain
DMARC (`v=DMARC1`). Inspect the returned values, not just command success. If the
operator's exact DKIM setup specifies a CNAME, query that same selector with
`--record-type CNAME` instead of treating a missing TXT answer as failure.

Each lookup reads the exact current domain version, then runs a bounded typed
DNS probe through the server's fixed public resolver under current authority.
Out-of-zone, stale or unavailable targets refuse; there is no local-resolver or
alternate-server fallback. Inspect `state`, `reason`, `perspective`, `checkedAt`
and `values`. NXDOMAIN, no answer and resolver failure are distinct outcomes.
Private or split DNS can differ from this public observation.

MX or TXT record presence does not prove SPF evaluation, a DKIM signature,
DMARC alignment, SES identity verification or recipient delivery. These commands
read DNS only; they neither change records nor send a diagnostic message. See
[Domains and DNS](domains-dns.md) for delegation and provider-record checks.

## Request one reviewed message

Choose an agreed recipient and keep a new UUID as `requestId`. Keep that nonce
and the original service, recipient, subject and body together in a private
request file. Reusing the same nonce and identical intent recovers the retained
test row; changing the service or content under that nonce is a conflict.
A lost reply or `unknown` result is not permission to generate a new nonce and
send again. Read history or recover the same request first.

Save this as `email-send.graphql`:

```graphql
mutation SendEmailTest($input: SendEmailDeliveryTestInput!) {
  sendEmailDeliveryTest(input: $input) {
    ok
    errors { code message field }
    data {
      id managedServiceId version requestId sender recipient
      accountId region identity transport status providerMessageId
      eventTrackingConfigured simulator createdAt acceptedAt observedAt reasonCode
    }
  }
}
```

Save a private `email-test.json`, replacing the GUIDs, reviewed service version
and recipient. Omitted subject and body use the server's diagnostic defaults.
Do not put real content or addresses into shared shell history or repositories.

```json
{
  "input": {
    "managedServiceId": "MANAGED_SERVICE_GUID",
    "expectedVersion": 12,
    "requestId": "ORIGINAL_REQUEST_UUID",
    "recipient": "agreed-recipient@example.com"
  }
}
```

```bash
astro --server staging --org ORGANIZATION_GUID api graphql \
  --file email-send.graphql --vars-file email-test.json
```

`--vars-file` supplies a JSON object; repeatable `--var name=value` flags override
individual variables. The CLI reports top-level GraphQL errors as failures, but
an HTTP 200 or CLI exit zero does not establish mutation success. Inspect
`sendEmailDeliveryTest.ok`, its structured `errors`, and the returned row.
A successful envelope can contain a failed, suppressed or uncertain test status.

The native send path verifies the current AWS account and tagged service-owned
SES identity, its sending verification, recipient suppression and the configured
feedback destination. Suppression is never bypassed, including simulator tests.
The native path confirms the account through STS. The configured role must allow
`ses:GetEmailIdentity`,
`ses:GetSuppressedDestination`, `ses:GetConfigurationSetEventDestinations` and
`ses:SendEmail` according to the actual installed IAM policy and resource
bounds. The test sends at most one native message request; ambiguous acceptance
is not retried. A received acceptance acknowledgement is retained before the
final public fresh-authority check, so a refused reply can still require
read-only recovery of the original row.

Native waits recheck current authority and the complete original binding; an
edit or withdrawal can refuse further work. A committed intent does not prove
that a request reached SES. Automatic retry of an uncertain send is disabled.

Subject is bounded to 200 UTF-8 bytes, with control characters refused; the text
body is bounded to 8,192 UTF-8 bytes. The server persists an intent digest,
sender/recipient, source metadata, status and provider message identity, not the
subject or body. Those content fields are excluded from application audit,
logging and tracing. Protect the local request file and the address-bearing
history response as private data.

## Read history and distinguish outcomes

Save this as `email-history.graphql`:

```graphql
query EmailTestHistory($service: GUID!, $after: String, $limit: Int! = 25) {
  emailDeliveryTestsPage(managedServiceId: $service, after: $after, limit: $limit) {
    items {
      id requestId managedServiceId version status sender recipient
      accountId region identity transport providerMessageId
      eventTrackingConfigured simulator createdAt acceptedAt observedAt reasonCode
    }
    nextCursor
  }
}
```

```bash
astro --server staging --org ORGANIZATION_GUID api graphql \
  --file email-history.graphql --var service=MANAGED_SERVICE_GUID --var limit=25
```

Follow the returned opaque `nextCursor` with `--var after=RETURNED_CURSOR`, keeping
the same organization and service. The default is 25 rows and the maximum is 50.
The legacy `emailDeliveryTests` list is bounded, but cannot traverse the whole
history; use `emailDeliveryTestsPage` for paging.

| Status | Meaning and next action |
|---|---|
| `submitting` | Intent exists; no confirmed acceptance. Read again with the original identity. |
| `accepted` | SES returned a message ID. This is provider acceptance, not recipient delivery. |
| `unknown` | Acceptance is uncertain, or submitting has aged past one minute. Preserve the nonce; do not blindly resend. |
| `suppressed` | The recipient is suppressed. Resolve the underlying condition through the appropriate operator process; the test does not remove suppression. |
| `failed` | Preflight or send failed; inspect the bounded reason and current source before any separately reviewed new test. |
| `deferred` | A signed matching provider event reports delivery delay. |
| `delivered` | A signed matching delivery event was observed; it does not prove a person read the message. |
| `bounced`, `complained`, `rejected` | Matching provider feedback reports the indicated result. |
| `observation_timed_out` | Accepted or deferred has aged past 15 minutes without a terminal observation. Delivery remains unconfirmed. |

`eventTrackingConfigured` means the accepted send observed an enabled matching
SNS destination, not proof that feedback will arrive. Test sends do not create or repair identities, configuration sets or event
destinations. An older missing configuration set requires normal reviewed
service revalidation/provisioning. Operators must configure
`SES_EVENTS_SNS_TOPIC_ARN` for the same account/region, the matching configuration
set and the installation's signed SNS callback path. Events must match the
original test, service, account, sender, sole recipient, topic and message ID.
Late matching events can update a timed-out display; duplicate events do not
create another send. History reads compute timeout states without proving a
remote failure or mutating a send into success.

## Installation and remaining limits

Resolve public delegation and SES identity verification separately; see
[Domains and DNS](domains-dns.md). A DNS check does not prove sender verification
or recipient delivery. An active managed-service status is a prerequisite,
not a delivery certificate. Native calls use fixed AWS service endpoints,
finite timeouts and a 30-second test-send budget. The current rate admission is
three tests per actor, ten per service and thirty per organization per minute;
its effective scope depends on the installed cache backend.

For the separate configured install SMTP channel, use the
[install alert SMTP guide](install-alert-mail.md). It tests only the operator's
own mailbox and does not fall back from this app-owned service.

This flow currently supports the exact AWS SES app-bound service. Azure email,
SMTP, global alert-channel tests and a fallback global mail transport are not
implemented here. Microsoft 365, Google mail and shared inbox management are
separate future work. Local proofs use controlled SDK/TLS transports and send
no external email; installation configuration and an explicitly authorized
real recipient test remain release/runtime acceptance work.

See [capabilities](capabilities.md), [shared services](shared-services.md) and
the [API reference](../reference/api.md) for discovery, binding and transport.
