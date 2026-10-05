# Connect domains and inspect DNS

A registered domain, a provisioned hosted zone and a working hostname are
separate things. Start in **Domains** and open the exact zone you want to inspect.
The detail page separates its configuration, provider records, recorded app
routes and runnable diagnostics. Checks include their observation time and
location; refreshing a check does not change DNS or registrar settings.

Use the [CLI](../reference/cli.md) with the selected server and organization, or
the [control API](../reference/api.md). Domain reads require the current domain
permission. Provider account inventory requires platform-operator authority.
Routing results contain only apps the current caller can read in the selected
organization. Missing access is never evidence that no records or routes exist.

## Connect Cloudflare in five steps

On an installation with `domains.cloudflare_connections`, open **Domains →
Connect DNS provider** (`/domains/connect`). From an existing domain's detail,
use its connection action; the wizard carries the exact domain GUID through
`/domains/connect?domainId=DOMAIN_GUID`. This is read-only provider discovery,
not a registrar connection or a DNS writer.

Setup requires an active installation Super-admin plus fresh selected-org
`provider_plugin.read` / `provider_plugin.configure` authority and credential
ceilings. Organization administration or staff status alone is insufficient.
Support flags describe current availability; every read and write rechecks the
actual actor, session or bearer and exact connection/domain versions.

1. **Provider:** choose Cloudflare. The Route53 option currently opens registered
   Route53 domain inventory using the installed provider identity; it has no AWS
   credential form or arbitrary existing-zone adoption flow.
2. **Connection:** give the connection a name. Use configured **Connect with
   Cloudflare** OAuth in the same selected-org browser session, or paste a
   zone-restricted API token with **Zone Read** and **DNS Read**. The token is
   write-only and encrypted at rest; it is cleared from the input before dispatch.
   If OAuth is unconfigured, the reason is shown and the API-token path remains
   available. Select the saved active connection after returning from OAuth.
3. **Zone:** select an existing Cloudflare zone from the complete inventory for
   that connection GUID/version. A forbidden, changed, oversized or incomplete
   inventory is not an empty successful account. Discovery is bounded to 400
   entries; a larger inventory is explicitly unavailable.
4. **Review:** inspect the exact zone, nameservers and normalized DNS records,
   including bounded TXT content. Review delegation separately and acknowledge
   the read-only limits. For a new domain, **Register** creates only a protected
   local `cloudflare_read_only` registration with default `NONE`, excluded from
   app routing and DNS-writer selection. For an existing same-zone registration,
   **Attach** adds the protected binding while preserving its existing DNS
   writer, TXT proof, configuration and app routes. It does not migrate them.
5. **Verify:** copy the returned TXT challenge and publish it yourself through
   the authoritative provider. Use **Verify ownership** when the current
   `canVerify` hint permits it, then open the domain's diagnostics. TXT observation
   does not provision routing, certificates or a provider zone. `canVerify` is
   separate from **Revalidate**, which uses its own current cluster action.

The wizard never changes registrar delegation, DNS records or certificates.
Its new read-only registration does not start a provisioning workflow. Auxiliary
attachment does not replace an existing writer's ordinary lifecycle. To manage
Cloudflare writes automatically, a connection-backed writer and reviewed
provisioning/certificate integration are still required.

### Configure OAuth as an operator

In Cloudflare **Manage Account → OAuth clients**, register a server-side
Authorization Code client with response type `code`, grant `authorization_code`
and token authentication `client_secret_post`. Register the exact HTTPS callback
URL below. Astrolift adds PKCE S256. Follow
[Cloudflare's client-registration instructions](https://developers.cloudflare.com/fundamentals/oauth/create-an-oauth-client/).

Obtain the two actual OAuth scope **IDs** corresponding to Zone Read and DNS Read
from Cloudflare's [OAuth scope catalogue](https://developers.cloudflare.com/api/typescript/resources/iam/subresources/oauth_scopes/methods/list/)
(`GET /client/v4/oauth/scopes`) and select them as required client scopes. These
are the returned `id` labels, not API-token permission UUIDs or guessed display
names. The public reference does not enumerate universal values; do not copy
illustrative test strings. Configure exactly two distinct read IDs.

| Installation setting | Required value |
|---|---|
| `ASTROLIFT_CLOUDFLARE_OAUTH_CLIENT_ID` | The registered client ID |
| `ASTROLIFT_CLOUDFLARE_OAUTH_CLIENT_SECRET` | Its secret from the installation's secret environment; never frontend configuration |
| `ASTROLIFT_CLOUDFLARE_OAUTH_READ_SCOPES` | The two verified scope IDs, separated by a space; both must end in `.read` |
| `APP_BASE_URL` | Exact public HTTPS app origin, with no path/query/fragment |
| Registered redirect URL | `https://YOUR_ASTROLIFT_ORIGIN/api/clusters/dns/cloudflare/callback/`, matching `APP_BASE_URL` |

Before accepting a token, configure a strong retained `DJANGO_SECRET_KEY` and
protect its backups. The currently registered secret-at-rest backend is
`local_fernet`; its encryption key derives from Django `SECRET_KEY`. Keep that
key available for retained ciphertext and outstanding OAuth attempts. Other cloud
secret backends are not implemented by this connection flow.

Before enabling OAuth, redact or omit this callback's query string in load
balancer, reverse-proxy and access logs. Never collect code, state, PKCE verifier,
client secret, bearer headers or provider error bodies in custom telemetry.
The application's URL-attribute scrubbing cannot redact upstream logs or browser
history. Tokens exchange only at the fixed Cloudflare endpoints described in
[Cloudflare's integration contract](https://developers.cloudflare.com/fundamentals/oauth/integrate-with-cloudflare/).

OAuth attempts bind the original browser actor/org/session and client, expire
after ten minutes and consume the encrypted verifier before one token exchange.
A replay, expiry or changed session/client refuses. An unconfirmed exchange is
not retried automatically. Refresh/offline access is not implemented; reconnect
when the bounded access token expires. Client registration and real OAuth
acceptance must be verified for the actual installation.

### Recover or disconnect without repeating an unknown write

If registration or attachment was accepted but its follow-up read failed, keep
the accepted receipt and refresh the domain/binding. If a reply is lost or
inconsistent, the wizard stops another write; read current Domains and the exact
binding before deciding what happened. Retesting a connection advances its
version, so explicitly rebind a domain after reviewing fresh versions.

Disconnect removes local access and wipes its ciphertext first. API-token
connections report `LOCAL_ONLY`; revoke the token at Cloudflare separately when
needed. OAuth attempts one remote revocation: `OAUTH_REVOKED` is confirmed, while
`OAUTH_UNCONFIRMED` still means local disconnection was accepted. Do not retry an
uncertain revocation or assume earlier reads were cancelled. Retained connection
and OAuth-attempt history prevents migration rollback; preserve it.

Use the existing GraphQL CLI for metadata recovery; never put a token in a query
example or shared shell history. Save this as `dns-support.graphql`:

```graphql
query DnsConnectionSupport {
  dnsProviderConnectionSupport {
    allowed reason apiTokenSupported oauthConfigured oauthSetupReason dnsWritesSupported
  }
  dnsProviderConnectionsPage(page: 1, pageSize: 20) {
    items { id version name state authMethod revocationState expiresAt dnsWritesSupported }
    totalCount page pageSize
  }
}
```

```sh
astro --server staging --org ORGANIZATION_GUID api graphql --file dns-support.graphql
```

For a known domain, save `dns-binding.graphql`:

```graphql
query DnsBindingRecovery($domainId: GUID!) {
  dnsProviderDomainBinding(domainId: $domainId) {
    domainId domainVersion state connectionId connectionVersion
    currentConnectionVersion zoneId zoneName canVerify dnsWritesSupported
  }
}
```

```sh
astro --server staging --org ORGANIZATION_GUID api graphql \
  --file dns-binding.graphql --var domainId=DOMAIN_GUID
```

An unavailable/changed binding cannot become a valid empty tuple. These are
read-only recovery operations; they neither grant write authority nor repair
DNS. The `domains` / `dns` offline topic includes the same setup instructions.

## Understand the stages

| Stage | What it proves | What to check next |
|---|---|---|
| Registered | Astrolift has a configuration row for the domain | Ownership verification and provider access |
| Ownership verified | The required TXT proof was observed, or verification was not required | Exact provider zone binding |
| Provisioned | The provisioning workflow reached its final configuration step | Public delegation and records |
| Delegation matches | The observed public nameservers match the configured provider nameservers | The intended hostname's DNS answers |
| DNS answer present | A resolver returned the requested record | Expected target, route and HTTPS |
| Route recorded | Astrolift has an authorized app/environment URL under the zone | The actual ingress and hostname response |
| HTTPS response observed | A public address accepted verified TLS for the hostname and returned an HTTP status | Application health and expected status |

The zone apex does not need an A or AAAA record if you only serve subdomains.
Check the hostname you intend to use. A wildcard also does not prove that every
name resolves: explicit records, delegation boundaries and TLS wildcard scope
can affect the result.

## Compare public delegation with the provider zone

The Overview separates the expected nameservers from the observed values. A
public Route 53 hosted zone can exist while your registrar still points the
domain at Cloudflare. In that case, records written to Route 53 do not become
the domain's public answers merely because the zone exists.

Choose the authoritative provider deliberately. If you keep the current
provider, connect and inspect that provider's zone. If you migrate, preserve
the existing records and review the registrar nameserver change before making
it. Changing nameservers can affect mail, verification records and other
services under the domain. The diagnostic tools perform no migration.

For Route 53, compare the four nameservers assigned to the **exact** public
hosted zone with the registrar delegation. See
[AWS's domain routing guidance](https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/dns-configuring-new-domain.html).
An `INSYNC` change means Route 53 propagated a change to its own servers; it
does not prove that the registrar delegates to those servers or that every
resolver has refreshed its cache.
[GetChange](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetChange.html)
describes that distinction.

A private hosted zone is resolved from its associated network. Public delegation
is not a health requirement for that zone. A public check cannot verify private
network visibility, and the page reports that limitation explicitly.

## Inspect records and routes

The Records view shows the provider inventory returned for the bound zone,
including ordinary record values and Route 53 alias targets. An alias may have
no ordinary TTL/value tuple. A provider permission error is shown as an error,
and an incomplete inventory is marked truncated. Neither is an empty successful
zone.

Use Routing to find the recorded app/environment URL, its cluster and ingress
class. Open the app to inspect its environment, ingress and certificate status.
A stored URL is a configuration association, not evidence that the load
balancer currently serves it. Shared zones do not expose another organization's
app routes. The routing list states when its visible result is incomplete.

## Run diagnostics

Select an exact hostname within the registered domain. Diagnostics do not
accept arbitrary URLs, resolver addresses or shell commands.

| Tool | Observation | Limits |
|---|---|---|
| Lookup / Dig | A typed DNS query through the fixed public recursive resolver | NXDOMAIN, no answer and resolver failure are distinct; the Dig control uses the same bounded DNS engine |
| HTTPS | Verified TLS with the hostname as SNI and one HTTP status line | Public address only, fixed HTTPS port, no redirect following or response-body collection |
| Ping | Bounded ICMP observation from the control-plane host | The utility or permission may be unavailable; a blocked ICMP response does not prove HTTPS failure |
| Traceroute | A bounded network path observation from the control-plane host | Routers may omit responses; unavailable utilities are reported explicitly |

DNS and network results represent the server's observation location, not your
browser's network or every internet resolver. The tools never report an
unavailable check as successful. Checks refuse unsafe network addresses,
out-of-zone targets, stale domain versions and unavailable permission.

## Use the CLI

First select the server and organization and list the zones:

```sh
astro operator domains list --org acme --json
astro operator domains show DOMAIN_GUID --org acme
astro operator domains check DOMAIN_GUID --org acme
astro operator domains check DOMAIN_GUID --hostname api.apps.example.com --record-type A --json
astro operator domains lookup DOMAIN_GUID --hostname apps.example.com --record-type NS --json
astro operator domains dig DOMAIN_GUID --hostname _dmarc.apps.example.com --record-type TXT --json
astro operator domains probe DOMAIN_GUID --hostname api.apps.example.com --tool https --json
astro operator domains probe DOMAIN_GUID --hostname api.apps.example.com --tool ping --json
astro operator domains probe DOMAIN_GUID --hostname api.apps.example.com --tool traceroute --json
```

Replace `DOMAIN_GUID` and the example hostnames with values from your selected
installation. `show` reads the exact domain without relying on the recent
200-row catalog. Each `check`, `lookup`, `dig` or `probe` reads the current domain
version and sends it with the observation request. A domain change during that
request refuses the result; rerun after reviewing the changed configuration.
Permission or schema errors do not trigger a local-network or alternate-server
fallback.

A completed report may contain a mismatch or an unsupported tool. Inspect its
typed state when using JSON in automation; process success means a report was
returned, not that every domain check passed.

## Use the API

Read the domain and its version:

```graphql
query DomainForCheck($domainId: GUID!) {
  astroliftManagedDomain(domainId: $domainId) {
    id version zone dnsDriver verificationState provisionState
    provisionNameservers provisionClusterId delegationCheck
  }
}
```

Carry that exact version into a fresh report:

```graphql
query CheckDomain($domainId: GUID!, $version: Int!) {
  astroliftManagedDomainDiagnostics(
    domainId: $domainId, expectedVersion: $version, recordType: NS
  ) {
    id version zone checkedAt
    checks { key state perspective checkedAt reason expected observed }
    providerZone {
      state reason checkedAt zoneId zoneName privateZone nameservers truncated
      records { name type ttl values aliasTarget aliasZoneId evaluateTargetHealth }
    }
    routes {
      appId appName appSlug environmentId environmentName recordedUrl hostname
      clusterId clusterName ingressClass observedState
    }
    routesTruncated
  }
}
```

Run a single lookup or network probe separately:

```graphql
query ProbeDomain($domainId: GUID!, $version: Int!, $hostname: String!) {
  astroliftManagedDomainProbe(
    domainId: $domainId, expectedVersion: $version,
    hostname: $hostname, tool: HTTPS, recordType: A
  ) {
    state perspective checkedAt reason hostname tool recordType values
    publicAddress httpStatus tlsVerified latencyMs
  }
}
```

States are `OK`, `MISMATCH`, `UNKNOWN`, `ERROR` and `UNSUPPORTED`. Preserve them
in your own display. A missing result or permission error cannot be converted
into a healthy empty inventory.

## Check email delivery separately

DNS lookup can inspect MX, SPF TXT, DKIM selectors and DMARC TXT records.
Expected values come from the configured sender or mailbox provider; do not
invent DKIM selectors or replace existing MX records to perform a test.

Existing app email-service panels expose the supported sender verification,
DNS authentication and delivery observations. The app overview also has an
explicit test-send action. Transport acceptance is separate from recipient
delivery: confirm which transport was used, then look for a matching supported
delivery or bounce event. A generic install-wide test does not certify an app's
different managed email binding.

See [exact email-service tests](email-delivery.md) for reviewed SES sends,
stable nonce recovery and signed observations.

Mail provider setup and a receive-capable inbox are separate capabilities.
Configuring an outbound sender does not allocate an inbox for an app or agent.
