# Messages between agents and people

An agent can send an update, ask a question, request an approval or report a
result. Recipients read and answer in Astrolift, the CLI or another client using
their own Astrolift identity. The client connects to Astrolift; you do not need
to expose an inbound endpoint on your computer.

Use [completion callbacks](agent-completion-callbacks.md) when a service needs a
signed final-task event. The inbox is for conversations with people.

## Check access and open the inbox

```bash
astro status --json
astro inbox --help
```

The installation must advertise `agents.messages`. The CLI must include the
inbox commands. Open **Inbox** in the dashboard to read threads and answer
inline. CLI clients can list messages, show a message, reply and follow new
messages. Check each command's help for the flags in your installed version.

```bash
astro inbox list --unread --limit 25 --json
astro inbox show <message-guid> --mark-read
astro inbox reply <message-guid> --choice <option-id> --request-id <request-uuid>
astro inbox follow --interval 5s --json
```

Use `--thread <thread-guid>` to narrow a list or follow to one thread. `list`
supports `--after <cursor>` for another page. `show` looks up the exact message
GUID; it does not substitute an agent slug or a different message.

For text, `reply --stdin` reads the body from standard input; `--body` is also
available. Reply text is limited to 4,000 Unicode characters. Repeat `--choice`
for multiple option IDs, or use `--decision allow`
or `--decision deny` for an approval. Keep the printed request UUID when a
reply's result is uncertain and reuse it with `--request-id` for recovery.
The command reports the server's recorded route, not completion of the agent's
next action.

`follow` polls the GraphQL inbox and emits JSON Lines with `--json`. It
deduplicates message IDs, bounds each catch-up read and retries transient
transport failures with backoff. Authentication or authorization errors stop
the command. It does not require an inbound callback or claim a WebSocket
connection.

Receiving a message does not grant access to its agent. The server checks the
recipient's current agent access and the credential's scope. Losing access
stops further reads and replies, including within a thread you previously read.

## Read messages through the API

```graphql
query AgentMessages($threadId: GUID, $after: String) {
  astroliftAgentMessages(threadId: $threadId, after: $after, first: 50) {
    nextCursor
    totalCount
    unreadCount
    items {
      id threadId kind source title body
      agentSlug agentName taskId taskStatus
      createdAt deliveredAt readAt expiresAt answerable
      options { id label description }
      attachments { kind ref label }
      replies { id authorLabel body choices decision route deliveredAt wakeTaskId }
    }
  }
}
```

Messages are returned newest first. Follow `nextCursor` to read more pages.
`unreadOnly: true` filters the message list. Use `astroliftAgentMessageThreads`
for the thread list; it returns each thread's latest message, message count and
unread count.

Clients can listen to `astroliftAgentMessageReceived` on the existing GraphQL
subscription transport, then refresh the affected thread. A subscription checks
current access throughout the connection. Reconnecting clients should refresh
their inbox and deduplicate by message ID.

The WebSocket endpoint is `wss://<install>/app/gql/config/ws/`, using the
`graphql-transport-ws` subprotocol. Send `connection_init` with
`{"authorization":"Bearer <user-token>"}` as its payload, wait for
`connection_ack`, then send a `subscribe` message containing the subscription
query. The token supplies the organization and scope ceiling. Do not put it in
the URL. A presented invalid bearer is refused rather than falling back to a
browser cookie. The subscription covers new messages; use the paged queries
for earlier history.

Mark only the messages or thread you actually read:

```graphql
mutation MarkRead($ids: [GUID!], $threadId: GUID) {
  markAgentMessagesRead(ids: $ids, threadId: $threadId) {
    ok
    errors { code message field }
    data { marked }
  }
}
```

Omitting both selectors marks all accessible messages read. An unread count is
separate from whether an agent received or acted on a reply.

## Answer a question or approval

```graphql
mutation Reply($input: AstroliftReplyToAgentMessageInput!) {
  replyToAgentMessage(input: $input) {
    ok
    errors { code message field }
    data {
      id messageId body choices decision route
      deliveredAt wakeTaskId createdAt
    }
  }
}
```

Pass the exact `messageId`, text in `body`, option IDs in `choices`, or `allow`
or `deny` in `decision` for an approval. Reuse one `clientRequestId` when
recovering an uncertain reply submission. Check `answerable`, the current
message and the server's response before assuming the agent can proceed.

Replies to a run's input or approval request go through the existing gate.
An approval requires the permission to send input to that run and follows the
configured action-safety policy. A read-only token cannot approve an action
merely because its owner has broader permissions.

For an active run, a reply can be delivered through its input channel. A reply
to an ended run may wake a new run when the person has dispatch permission, or
remain queued for the agent to collect. Read the reply's `route`, `deliveredAt`
and `wakeTaskId`; accepting the reply is separate from the agent completing
another action.

## Let an agent send messages

Astrolift supplies the workload's message and run-MCP endpoints as
`ASTROLIFT_MESSAGES_URL` and `ASTROLIFT_RUN_MCP_URL`. These endpoints use the
run's own callback identity. A person's API token or a dispatcher key does not
become that run's identity.

The run-scoped MCP server exposes `astrolift_message_user` and
`astrolift_get_message_replies`. Keep its credentials inside the workload.
Do not paste them into messages or give them to a desktop client. A person uses
the GraphQL inbox with their own token.

An agent may address only recipients who can read it. An unknown or disallowed
recipient refuses the send. Questions can carry option IDs and an expiry;
attachments are references to supported artifacts, installation URLs or
semantic-layer citations. Keep message bodies concise and avoid secrets.
Redaction on the send path is a backstop, not a reason to include credentials.

An attachment has `kind`, `ref` and an optional `label`. Supported kinds are
`artifact`, `citation` and `url`. URL references must be installation-relative
paths or HTTPS URLs on the installation's configured host. A message accepts
up to ten attachments; each reference is at most 1,000 characters and its label
at most 200. A reference alone does not prove that the reader can retrieve its
content; resolve it through the corresponding authorized resource API.

## Notification channels and retention

In-app delivery and read state belong to each recipient. Existing notification
preferences control available push and email fan-out; the installation must
also have those channels configured. Webhook subscriptions can select the
`agent.message.sent` event. A channel accepting a notification does not prove
that the recipient read it.

The purge command uses the organization's retention policy. Operators can run
`manage.py purge_agent_messages` to purge expired history and should include it
in their maintenance schedule. The command's availability does not establish
that a periodic purge has already been installed.
