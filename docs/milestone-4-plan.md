# Milestone 4 plan

## Milestone 4A — Real incoming plain-text messages

Status: ready for automated review and real-message manual validation. Do not
mark complete until a linked account has received and displayed real messages.

Milestone 4A connects the existing long-lived whatsmeow client to walite's
existing bounded service pipeline. Direct `events.Message` conversation text
and extended-text bodies are copied into `model.Event` values, committed by the
existing realtime writer, published through `Core.LiveEvents`, and applied by
the existing TUI live-message path. The WhatsApp chat JID is the stable chat ID
and temporary presentation title when no chat metadata is available.

The connection-owned adapter has a fixed 64-event working queue. A full queue
applies cancellable backpressure to the whatsmeow callback; it does not create
per-message goroutines, grow storage, or silently discard recognized text.
Connected production mode starts with an empty memory-backed snapshot and does
not compose `FakeSource` or `OfflineTextSender`. Walite-originated sends were
disabled in 4A; 4B below enables plain text using the same client and data path.

Explicitly excluded from 4A:

- WhatsApp history sync
- media and body placeholders
- contact names and group metadata
- real outgoing sends
- reply transport semantics
- edits, deletes, reactions, receipts, presence, typing, calls, and status
- production SQLite cache composition

## Milestone 4B — Real outgoing plain-text messages

Status: ready for review and the manual real-send acceptance test after automated
validation. Not complete or committed; no real message has been sent by automated
work. PN/LID send/echo alias routing is included in this review.

User manual validation now confirms plain/Unicode outgoing delivery, one recipient
copy, one local copy, and no observed PN/LID duplicate. The quoted-reply follow-up
below still requires its own manual recipient-side test; it is not yet certified.

`TestRealTextSendPNAndLIDEchoCommitOnce` was reproduced failing before alias
routing (a second committed message under the LID). It now passes without changing
generic store identity, which remains ChatID + MessageID. The adapter policy below
preserves the first established presentation identity, including when the echo
arrives before the outgoing transport result.

### Ownership and upstream API

`Connection.TextSender()` exposes a walite-owned `ValidateText`/`SendText`
capability backed by the **same** long-lived whatsmeow client as authentication
and incoming traffic. It does not open another store or connection. Connected
composition is that source + sender, `store.Memory`, `service.Core`, its existing
writer and committed `LiveEvents`. No `FakeSource` or `OfflineTextSender` is used
in connected production; both remain available to offline tests/fixtures.

Verified against the pinned module
`go.mau.fi/whatsmeow v0.0.0-20260828224850-0fadda796019` and read-only reference
`../references/whatsmeow` at `662ad1dc6900ffe1b1a2a6bc0fca01cba488d747`:

```go
func (*Client) SendMessage(context.Context, types.JID, *waE2E.Message,
    ...SendRequestExtra) (SendResponse, error)
```

Reference: `send.go` (`Client.SendMessage`, `SendResponse`), `types/jid.go`
(`ParseJID`), `request.go` (`Client.retryFrame`), `client.go` (`IsConnected`,
`IsLoggedIn`, `Disconnect`), licence MPL-2.0. The inspected reference `send.go`
is byte-identical to the installed pinned module. Adapted only the documented
plain `Conversation` field/API usage; no implementation files were copied.

For plain sends, only `waE2E.Message{Conversation: &text}` is supplied: URLs remain ordinary text,
without previews or optional metadata. Inside `internal/wa`, `ParseJID` is
followed by canonical round-trip, nonempty-user, non-device and server checks.
`@lid`, `@s.whatsapp.net`, and `@g.us` are supported; no telephone-number
derivation or application-side group metadata lookup is added. Upstream handles
its required group participant/encryption work. Invalid identifiers, empty or
oversized/invalid-UTF-8 text, and disconnected/unauthenticated clients are rejected
before `SendMessage`. Whatsmeow types never cross `internal/wa`.
The UI preflight reads an atomic connection-event readiness hint, not upstream
`IsConnected` (whose socket lock can be held by a reconnect). The worker alone
rechecks the real socket/authentication state immediately before transport.

### Bounded PN/LID routing inside the WA adapter

Confirmed identity information in the pinned source and reference above:

- `send.go:Client.SendMessage` accepts the selected conversation JID and may
  replace a PN destination with its LID internally. `SendResponse` supplies the
  message ID, timestamp and **local sender**, not a canonical destination.
- `types/message.go:MessageSource`, embedded in `MessageInfo`, contains `Chat`,
  `Sender`, `SenderAlt`, `RecipientAlt`, `AddressingMode`, `IsFromMe`, and
  `IsGroup`. There is no current `Info.Recipient` field. `Chat` can be PN or LID.
- `message.go:Client.parseMessageSource` populates `RecipientAlt` for a from-me
  direct-message recipient; `SenderAlt` describes the other party for an incoming
  direct message. A group sender/participant is never a group-chat alias.
- `store/store.go:Device.GetAltJID` and `LIDStore.GetLIDForPN/GetPNForLID` read the
  existing authoritative session mapping. `store/sqlstore/lidmap.go:CachedLIDMap`
  implements that lookup without contact discovery or networking.
  `store/sqlstore/container.go:Container.initializeDevice` initializes the LID
  store when the device is loaded/saved; fresh unlinked devices have none yet.

These reference symbols are MPL-2.0. The checked-out identity type file is
byte-identical to the pinned module; the other files have unrelated differences,
but the inspected parsing/lookup/initialization symbols agree. Only API use and
field interpretation were adapted, not upstream implementation code.

The connection owns one fixed **128-record** routing table shared by its existing
source and sender. Each record contains the first established opaque ChatID and
at most one authoritative alternate. No message IDs, text, contacts, or names
are cached. This is a deliberately small session bound, matching the default
realtime admission entry count, not the architecture's global 10,000-chat cache
limit. It leaves headroom beyond the unchanged **16-chat TUI** working set without
retaining a whole address book. IDs retain their existing 512-byte maximum:
at most 128 KiB of identifier payload plus fixed headers. Group IDs bypass the
table. The existing 64-entry source ring carries one optional bounded alternate
per entry (at most 32 KiB extra identifier payload); event/text bounds are unchanged.

Incoming callbacks copy the recognized text and the applicable event alternate;
they do no lookup I/O. On the existing source worker, explicit event metadata is
preferred, otherwise `Device.GetAltJID` reads the existing mapping. The sender
pins the requested ID and learns any available mapping **before** its one remote
call, after service capacity reservation. Thus the first PN or LID identity stays
stable even if the echo commits before `SendMessage` returns. A shared mutex
protects scans of at most 128 records; lookup I/O never runs under that mutex. No new
worker, contact synchronization, network lookup, or goroutine per identity exists.

Only authoritative opposite PN/LID pairs are aliases. Similar numbers or matching
message IDs do not imply equivalence. Events are readdressed to the established
opaque ID before generic service/store admission, with Unicode, message ID,
timestamps, FromMe, body flags and byte accounting preserved. The model addition
is only `Message.WithChatID`, an immutable validated copy helper; model/service/
store/TUI have no WhatsApp suffix rules. The sender returns the original requested
ChatID, preserving `Core.SendText`'s existing acceptance contract. A caller trying
to send through a known non-primary identity is rejected before transport; the
normal TUI only presents the established primary.

**Deterministic capacity/conflict policy: no eviction or replacement.** Forgotten
routes could split a still-selected or retained chat on its next echo. The WA
adapter cannot safely select an eviction victim without a larger cross-layer
ownership policy, so a new 129th direct identity returns `ErrChatAliasesFull`.
Known routes still resolve, and adding an alternate to an existing record consumes
no extra slot. A lookup failure or two previously independent established routes
later proved equivalent returns a controlled error, never a silent merge. Incoming
resolution errors stop the source and surface through the existing service failure
path; outgoing resolution errors occur before transport. Cancellation is preserved
as cancellation. Tests prove no table mutation on rejected replacement/conflict.
Merging two already-visible independent chats remains unsupported; this increment
only resolves the approved send/event-time alias case, not arbitrary chat merging.

Existing store idempotency now sees the same ChatID + MessageID for successful
sends and authoritative alias echoes. Different message IDs remain separate, as
do unrelated chats sharing a message ID. No duplicate LiveEvent means no second
chat or perturbation of selection, draft, cursor, reply, viewport, unread/activity
or popups. The TUI also retains its existing duplicate-identity guard unchanged.

### Bounded admission and draft semantics

Enter calls only the application's local admission callback. One owned sender
worker has a **one-request mailbox**, a **one-result mailbox**, and a single
queued/in-flight slot. An unconsumed result also prevents further admission.
There is no goroutine per send, unbounded queue, optimistic message insertion,
or walite retry loop. Each admitted request is bounded by the existing model
text/identifier limits. The worker uses the same `Core.SendText` acceptance path:
it acquires the one-send slot and reserves a realtime queue entry plus
`MaxNormalizedEventBytes` **before** invoking transport. Failure releases that
reservation; success converts it into the normalized event without a subsequent
queue-full decision. Saturation therefore makes zero transport calls.

The draft/cursor/reply/viewport are unchanged by preflight rejection. Rejection
is visible in the footer. After worker admission the draft remains visible but
protected from editing, Escape cancellation, and repeat Enter; scrolling,
settings, resize, incoming events and Ctrl-C remain responsive. Remote success
plus realtime admission clears the protected draft and returns the viewport to
newest. It does not directly append a message. Offline direct synchronous TUI
fixtures retain their previous callback semantics.

Worker completion is not persistence confirmation: the message appears only
from the normal `WriteRealtime -> LiveEvents -> TUI` path. Authoritative response
`ID` and server `Timestamp`, original ChatID, exact text and `FromMe=true` form
the event. Missing/invalid successful-result metadata is treated conservatively
as delivery unknown, never replaced by synthetic identity/time.

### Errors, cancellation, echo and shutdown

Before transport, busy/unavailable/validation failure retains an editable draft.
Once `SendMessage` has been invoked, any error is conservatively **delivery
unknown**: the server might have accepted it before acknowledgement was lost.
No fake success is created and no automatic retry occurs. The text stays visible
and Enter stays disabled; the footer asks the user to check the recipient.
Escape explicitly discards that protected draft, without sending anything.
There is no promise of exactly-once remote delivery across unknown outcomes or
deliberate re-composition by the user. Upstream errors are not printed with
potentially private message/identifier contents.

The caller context controls admission. Once admitted, the application-owned
worker context controls execution, with a 30-second request deadline. Cancelling
before execution prevents transport. Cancelling during transport produces no
retry; a successful `SendMessage` return wins over concurrent cancellation and
still consumes its existing realtime reservation. Successful transport cannot
become ordinary Busy. Ambiguous cancellation/invariant errors also protect the
draft against resubmission.

Whatsmeow itself may retry the same encrypted frame/message ID once after a
disconnect; walite does not call `SendMessage` twice or generate a new retry ID.
Its reconnect wait can take up to five seconds beyond immediate cancellation,
and its internal send mutex is not context-aware. Consequently the authenticated
view cancels the **connection lifecycle before joining the send worker**.
The existing single `Disconnect` owner releases response waiters, including
internal peer sends. The worker is joined before closing the session store;
no result producer sends on a closed channel. No new connection is created
from the send path. The 30-second request deadline is not a strict wall-clock
bound on upstream mutex/reconnect handling.

Bounded shutdown cutoff: fatal service failure or shutdown can stop local
commit/publication before a remotely successful message is observed. The
existing service shutdown grace (two seconds), final-batch limits and committed
stream cancellation remain unchanged. A result after that cutoff is not an
ordinary retryable rejection; walite does not claim it was never sent. Memory
is still the application cache, so this increment offers no durable outgoing
recovery across process exit. Do not resend blindly after quitting mid-send.

Outgoing success and its incoming echo use the same stable ChatID after adapter
alias routing, and the same WhatsApp MessageID, in either arrival order.
Existing store idempotency publishes one committed event;
the existing bounded TUI identity check also prevents a second visible copy.
No additional deduplication cache is introduced. This is the existing retained
working-set idempotency boundary, not an unlimited lifetime delivery ledger.

### Validation and manual gate

Deterministic, network-free tests cover real-adapter request/result mapping,
Unicode and opaque/group JIDs, invalid/disconnected rejection, source/sender
client sharing, blocked/cancelled sends, success/cancellation races, fixed
mailboxes and single admission, pre-transport reservation under saturation,
reservation release, the committed path and incoming echo, protected drafts,
responsive settings/resize/quit, and disconnect-before-worker-join. Existing
QR/reconnect, incoming 4A and offline tests must remain green.

Alias regressions additionally cover both PN/LID directions, echo-first and
result-first commits, aliases with different message IDs, unrelated chats with
the same ID, preexisting PN/LID presentation identity, metadata-first versus real
local session-store fallback, Unicode and body-flag preservation, concurrent table
access, fixed capacity/conflict failure, cancellation before transport and all
transient TUI state with a full 16-chat working set. The original
`TestRealTextSendPNAndLIDEchoCommitOnce` must pass uncached.

Alias follow-up validation: gofmt, `go mod tidy`, `go mod tidy -diff`,
`go vet ./...`, `go test ./...`, `go test -race ./...`, focused uncached
outgoing/alias tests (also with race detection), and `git diff --check` pass.
Module files are unchanged. The first full race run hit the unchanged SQLite
queue-saturation test's timing-sensitive remaining-batch assertion; its isolated
race rerun and the subsequent complete race suite pass. No store changes were
made to obtain that result. No real connection/send or commit was performed.

After automated validation, manually send `walite outgoing test 👋` and
`é äöü 日本語 🐧` to a known chat. Confirm responsive UI, exactly one recipient
copy and one local committed copy, correct timestamp and FromMe presentation.
An ordinary group JID can be checked separately. Automated tests must never
connect or send a real message.

Explicit exclusions: names, read receipts, group/cross-chat replies, media, reactions,
edits/deletes, history sync, typing, link previews, formatting conversion and
application-cache SQLite wiring. The proven QR raster and incoming text filtering
are unchanged; authoritative chat-alias routing is a separate adapter step.

## 4B follow-up — One-to-one quoted text replies

Scope: same-chat, one-to-one replies to currently retained text, including the
local user's own text. Plain sends still use only `Message.Conversation`.
Group replies are deliberately unavailable: the existing model retains FromMe,
not the actual group participant that authored each message. No participant is
fabricated, no names are resolved, and no group metadata is fetched.

### Confirmed protocol shape

Checked the current pinned module listed above and the read-only whatsmeow
reference. `send.go:Client.SendMessage` documents reply metadata in `ContextInfo`.
`proto/waE2E/WAWebProtobufsE2E.pb.go:ExtendedTextMessage` and `ContextInfo` expose
the following fields with matching types/tags in both versions (MPL-2.0):

```text
Message.ExtendedTextMessage
  Text = exact outgoing reply body
  ContextInfo
    StanzaID = exact quoted message ID
    Participant = quoted author (not always the outgoing sender)
    QuotedMessage.Conversation = bounded retained-text excerpt
```

`RemoteJID` is intentionally absent for these same-chat replies; the existing
SendMessage destination supplies the chat. No cross-chat quote is supported.
The reply-context pattern was also checked in read-only nchat,
`lib/wmchat/go/gowm.go:WmSendMessage` (MIT). Only the small field/API relationship
was adapted; no implementation file or logging behavior was copied. Protocol
construction remains entirely in `internal/wa`.

Chat ID plus message ID alone does not identify which side authored the quote.
The TUI already has the quoted message's stable ID, retained text and FromMe flag.
For an incoming direct-message quote, the author is that conversation's peer:
use the peer JID, preferring its authoritative LID when the existing alias lookup
provides it. For an own-message quote, read the **same** client's device
`GetLID`, falling back to `GetJID().ToNonAD()` (confirmed in
`store/store.go:Device.GetLID/GetJID`, MPL-2.0). Missing own identity fails before
transport. The established presentation ChatID does not change.

### Minimal data and safe acceptance

The only model addition is immutable, transport-neutral `TextQuote`: exact
message ID (existing 512-byte bound), FromMe, and a UTF-8-safe text excerpt capped
at **1 KiB**. Longer retained text is excerpted without changing the new reply
body. Originally send-only, it is now also retained by the local presentation
follow-up below. It is not a stored protobuf, message snapshot, new SQLite cache
column, incoming context parser, or generic threading model. Unavailable/evicted
or bodyless targets cannot fabricate a quote; they produce controlled rejection
with draft/target preserved. Existing TUI reply selection and preview remain.

`SendTextRequest` and the existing sender interface accept at most one quote.
The same capacity-one worker/request/result mailboxes carry the owned excerpt;
no extra worker, client, queue or separate reply sender exists. Total variable
request data stays bounded at 18 KiB (16 KiB body + 1 KiB excerpt + two 512-byte
IDs), below the existing 32 KiB ceiling. `Core.SendText` still reserves its one
realtime slot and maximum event charge before invoking transport. Reservation,
cancellation, successful-result validation, writer/store and LiveEvents semantics
are unchanged. The offline fixture accepts plain text only, as before.

Group quote preflight returns the safe visible message "Group replies not
available yet; draft kept" without invoking transport. Before-admission failure
keeps draft, cursor, target and viewport. During send the draft/target stay
protected. Transport errors remain delivery-unknown with no automatic retry and
no synthetic outgoing message. Success clears the draft and target through the
existing completion path; only committed LiveEvents display the message. Quoted
echoes use the unchanged extended-text ingestion and PN/LID alias deduplication.
Incoming quote parsing and SQLite quote persistence remain outside this increment.

### Reply validation and manual gate

Network-free tests verify exact protobuf/context fields, destination, PN/LID/own
participant identities, Unicode, one transport call, group/missing-data rejection,
the bounded excerpt, worker propagation, reservation before transport, saturation
making zero calls, transport failure, success racing cancellation, draft/target
preservation, and one committed message after an alternate-identity reply echo.
Normal incoming, plain outgoing, authentication, reconnect and race suites must
also remain green.

Reply follow-up automated validation passed: formatting, `go mod tidy`,
`go mod tidy -diff`, `go vet ./...`, `go test ./...`, `go test -race ./...`,
focused uncached reply/plain-send regressions, and `git diff --check`.
The complete race run passed without retries. `go.mod` and `go.sum` are unchanged.

After automated validation, select an incoming text in a real one-to-one chat,
compose `walite reply test 👋`, and send through the existing reply-selection UX.
The recipient must see a real quote; walite must remain responsive, show one
outgoing message, and clear the reply target after success. Group replies are
not part of this retest. No real send or commit is performed by automated work.

## 4B follow-up — Local committed reply presentation

Manual testing confirmed correct remote quoted replies, exactly one remote/local
message, and no PN/LID split; the local copy incorrectly looked like plain text.
The previous offline renderer retained `messageView.hasReply/replyToID` and
looked up the original message. The real sender used `TextQuote` for its wire
payload but built its success `model.Event` without that metadata. The generic
message and presentation adapters previously had nowhere to retain it.

After transport success, `Core.SendText` now attaches the already validated
request's `TextQuote` to the transport-owned result. Identity, timestamps,
transport/protobuf/context, reservation-before-send, cancellation, worker and
result/draft semantics are unchanged. The only visibility path remains:

`success → Event.Message.Quote → realtime writer → Memory.WriteRealtime →
LiveEvent.Message.Quote → cmd adapter → tui.LiveMessage reply fields → existing renderer`.

Message constructors/copies, batch and event accounting retain and charge the
exact quoted ID, quoted FromMe and UTF-8-safe excerpt (at most 1 KiB). A maximum
quoted event still fits the existing 32 KiB reservation; event/queue counts do
not grow. The bounded batch byte ceiling and minimum writer-queue validation
include the extra bounded metadata. Body pruning also removes the quote rather
than retaining private excerpts after discarding a body.

The connected backend is still `store.Memory`; this fix adds no SQLite schema or
production SQLite wiring. Memory retains an established quote when a duplicate
echo omits it, including duplicates in the same batch, without another insert,
unread increment, or LiveEvent. Snapshot and live presentation share plain
`ReplyToID/ReplyToText/ReplyToFromMe` fields, preserving the TUI's enforced
independence from application model types. Rendering uses the existing `↪` line
and timestamp layout; the bounded excerpt remains available when the original
has left the working set.
Legacy offline reply rendering remains unchanged; there is no optimistic append.

Separate existing limitation: an echo that commits **before** the successful
send result can publish a quote-less message first. The later duplicate can
enrich Memory, but insert-only LiveEvents do not emit metadata refreshes and the
TUI does not upgrade duplicates. Solving that ordering edge requires a separately
approved narrow enrichment change; this patch does not reopen that contract.

Deterministic tests cover exact Unicode/FromMe/ID propagation, the 1 KiB bound,
maximum byte charges, committed/snapshot adapters, simulated-screen parity with
offline replies, missing-original fallback, plain/reply rendering through the
connected application worker/Core/Memory/live adapter, and PN/LID duplicate
echo preservation with one transport call and one committed outgoing message.

Manual retest: select a one-to-one incoming message, reply with
`walite reply presentation test 👋`, and verify the quote on both devices,
exactly one local/remote message, and a cleared reply target. No real sends or
commits are performed by automated validation.

Final validation passed: `gofmt`, `go mod tidy`, `go mod tidy -diff`,
`go vet ./...`, `go test ./...`, `go test -race ./...`, and `git diff --check`.
`go.mod` and `go.sum` are unchanged. During validation the enforced TUI dependency
test prompted plain presentation fields instead of a model import; a race in
the new screen test was fixed by capturing immutable frames on the drawing
goroutine (five consecutive focused race runs passed). The unchanged SQLite
batch-timing test also flaked during the first full race run; it passed in
isolation and in the final full race suite. SQLite code/tests were not changed.
