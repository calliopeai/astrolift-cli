# Read platform notifications

Platform notifications include app-expiry warnings addressed to the current
person. They are separate from [agent conversations](agent-inbox.md), task events
and completion callbacks. Reading them does not mark an agent thread read.

Require `notifications.structured_inbox` in server discovery. Use an authenticated
current person in the selected organization. API credentials need current app-read
scope and remain limited by their organization and team. The server rechecks
membership, credentials, current app access and warning preferences on every
query and stream poll.

## Catch up and follow

```graphql
query CatchUp($after: String) {
  astroliftMyNotificationsPage(after: $after, limit: 100) {
    items {
      id kind title body link readAt createdAt
      expiryWarning { appId appSlug expiresAt warningWindow }
    }
    endCursor
    hasMore
  }
}

subscription Follow($after: String) {
  astroliftMyNotificationStream(after: $after) {
    items {
      id kind title body link readAt createdAt
      expiryWarning { appId appSlug expiresAt warningWindow }
    }
    endCursor
    hasMore
  }
}
```

`after: null` starts at the beginning of retained history. Process a whole batch,
persist its `endCursor`, and continue while `hasMore` is true. Deduplicate by
notification `id`. The query scans at most 100 rows per batch, default 50.
Authorization can hide rows, so an empty batch can still advance its cursor.
Always retain the new cursor and obey `hasMore`.

The subscription drains catch-up batches and then checks the database every two
seconds. An initial empty batch supplies a cursor for an empty inbox. After a
disconnect, reconnect with the last fully processed cursor. Reads are at least
once while the notification is retained and the person remains authorized; there
is no client acknowledgment or indefinite retention promise.

The WebSocket endpoint is `wss://<install>/app/gql/config/ws/`, using subprotocol
`graphql-transport-ws`. Send `connection_init` with payload
`{"authorization":"Bearer <person API token>"}`, or use an existing authorized
browser session. Wait for `connection_ack` before subscribing. Keep credentials
out of URLs. Authorization errors terminate the operation.

The existing `astro api graphql` transport can execute the catch-up query with
the person's configured credential. This API adds no CLI streaming command. The
legacy `notificationReceived` subscription reads a different notification store
and is not an expiry-warning feed.

## Cursors, visibility and warning history

A cursor is signed and bound to the installation, organization, person and
credential ceiling. A different credential, team or scope set must restart with
`null`. Invalid cursors are rejected. Gaining access does not rewind an existing
cursor: restart with `null` to rescan retained history and deduplicate IDs.

Expiry warnings require current app-read access, current creator/team-recipient
eligibility and no explicit per-app `app_expiring` mute. Warnings disappear from
these reads when their app or owner becomes unavailable. Team-bound credentials
see only notifications whose structured provenance proves their app scope.
Historical warnings without that provenance are omitted; the server does not
infer authorization from human text or links.

`expiryWarning` records the app's GUID and slug, absolute expiry, and emitted
window (`24h` or `1h`). Only the most specific due unsent window emits; missed
earlier windows do not create additional warnings. Extending an expiry does not
rewrite old rows. Display the recorded expiry as historical warning context and
fetch current app state before offering an extension.

Reading or streaming does not change read state. Use `markNotificationRead` or
`markAllNotificationsRead` explicitly. Read-state changes are not new stream
events. Personal quiet hours suppress agent-message email/push only; app-expiry
warnings keep their own delivery preferences. No inbox receipt proves an external
channel delivered a message.
