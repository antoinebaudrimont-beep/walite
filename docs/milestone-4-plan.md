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

## Milestone 4C — Contact and group display names

Status: implemented for review; **manual name/group validation pending**. Not
complete and not committed. The starting tree was clean on
`milestone/4-live-whatsapp`, at `ba78874` (completed real outgoing 4B work).

### Confirmed metadata sources

Checked the pinned whatsmeow module
`v0.0.0-20260828224850-0fadda796019` and read-only reference
`../references/whatsmeow` at `662ad1dc6900ffe1b1a2a6bc0fca01cba488d747`.
The following API/field relationships agree in both versions (MPL-2.0).
Only API use and field interpretation were adapted; no upstream implementation
files were copied:

- `store/store.go:ContactStore.GetContact(ctx, JID)` and
  `store/sqlstore/store.go:SQLStore.GetContact`: local device-store/cache reads,
  not contact discovery. `types/user.go:ContactInfo` supplies `FullName`,
  `FirstName`, `BusinessName`, and `PushName`. A missing record is not an error
  requiring a network fetch. No `GetAllContacts` call is made.
- `store/store.go:Device.GetAltJID`: the existing local PN/LID mapping, as in
  4B. A fresh/unlinked device's absent stores remain guarded. Name resolution
  never modifies walite's alias registry.
- `types/events/appstate.go:Contact`, `PushName`, `BusinessName`: naturally
  delivered saved-name changes (`Contact.Action.FullName/FirstName`), push-name
  changes (`NewPushName`, `JIDAlt`), and verified business-name changes
  (`NewBusinessName`). Full-sync contact events are ignored; no synchronization
  is enabled/requested to obtain names.
- `types/events/events.go:GroupInfo.Name` and `JoinedGroup.GroupInfo.Name`:
  naturally delivered authoritative group subjects. `types/message.go` supplies
  `MessageInfo.PushName` and embedded `MessageSource.Chat`, `Sender`, `SenderAlt`,
  `RecipientAlt`, `IsFromMe`. An actual group chat is identified inside WA by
  `Chat.Server == types.GroupServer`, not by a generic layer parsing JIDs.
- `group.go:Client.GetGroupInfo(ctx, JID)`: an explicit **network lookup of one
  observed group** when its subject is not known. There is no exported local
  subject-store reader in this API. Only the returned matching group's `Name`
  crosses the adapter; walite does not enumerate groups or retain participants.
  Upstream itself parses participants, maintains its routing cache, and saves
  authoritative LID/redacted-phone mappings as part of this call. This is not
  a names-only wire request or an application-controlled response-byte bound.

All whatsmeow/protobuf/JID types remain inside `internal/wa`. Tests substitute
the contact/group/alternate lookup functions and never connect to WhatsApp.

### Identity, source priority and aliases

`model.DisplayMetadata` is an immutable advisory value: opaque ID, at most the
existing **1 KiB** display-name limit, source quality, and group flag. Names
are normalized to valid UTF-8/single-line text. No chat/message is keyed by a
name. Two people with the same name remain separate.

Person-name priority is deterministic:

1. saved `FullName`, then saved `FirstName`;
2. verified business name;
3. authoritative push name;
4. `+` plus digits from a validated PN JID (at most 15 digits);
5. original opaque ID, including unknown LIDs.

LID digits are never interpreted as a phone number; redacted phone strings are
not promoted to a full phone number. Group subject is a separate highest rank
used only for groups. Empty/lower-quality updates cannot erase a known name.
Equal-quality live observations rename the label; equal-quality local PN/LID
contact-record ties prefer the PN record regardless of request order. A live
update arriving during a lookup wins over its stale result.

Lookups read 4B's existing bounded alias table and, when necessary, the local
`Device.GetAltJID` mapping. Event-provided `SenderAlt`/`RecipientAlt` pairs are
preferred. A label is offered for both authoritative spellings; the existing
chat keeps its established ChatID. There is no second alias registry, inferred
name-based merge, identity-slot consumption by group participants, or alias
eviction. The 4B identity capacity/conflict policy is unchanged.

### Message and advisory-update paths

Incoming group text now retains bounded generic `Message.SenderID` and
`Message.IsGroup` through `Event -> WriteRealtime -> Memory -> LiveEvent -> cmd
adapter -> TUI`. Sender identity is included in constructor/copy/pruning/batch
byte accounting. Maximum writer-message validation includes that extra bounded
ID; existing event reservations and queue counts are unchanged. Duplicate
messages retain established sender/quote data without another committed insert.

Names arrive through a **separate advisory metadata channel**, not a new kind
of committed message. `cmd/walite` owns its cancellable forwarding goroutine:
apply the label to `Memory`, then send the accepted metadata to the TUI. Memory
updates display fields only and caches labels for not-yet-created chats/people.
It does not insert chats/messages or change unread counts, activity order,
timestamps or selection. Existing chats retain their known quality even when
an advisory cache slot is replaced.

TUI snapshot/live fields remain plain presentation values, preserving its
enforced model/service/store/WA import boundary. A late name updates the chat
list/header and retained matching group-sender labels without rebuilding state.
Draft, cursor, reply target/selection, viewport, emoji/settings popups and
stable IDs survive. Direct message bodies do not repeat the chat name. Incoming
group messages have a compact bold sender line aligned with the existing
timestamp/body layout; unknown participants use their opaque SenderID (or
`Unknown sender` if identity was absent). From-me messages omit that extra label.
Group sender rows are included in viewport measurement. Existing grapheme/cell
truncation, unread suffix reservation, full-frame clearing and narrow layout
are reused. Duplicate/lower-quality/empty metadata produces no redraw; changed
presentation produces one frame, with no polling/idle redraw.

The backend remains Memory. No SQLite schema, production SQLite composition,
or persistence of these new fields is claimed. Reconnect/authentication, real
send/protobuf behavior, bounded send worker, reservation-before-transport,
reply handling and committed LiveEvents semantics are unchanged. Group replies
remain unavailable despite the new presentation-only sender identity.

### Bounded lookup and fallback policy

- One connection-source-owned lookup worker, cancelled and joined on exit;
  no goroutine per message or lookup. WhatsApp callbacks perform no lookup I/O.
- Fixed **32 pending requests**, plus one in flight, deduplicated across known
  PN/LID pairs. Reversed pairs remain intact. A newly learned alternate during
  an in-flight lookup may schedule one coalesced follow-up. Overflow keeps older
  pending work; a subsequent observation may request the missing label again.
- Fixed **128-entry FIFO name caches** in the adapter, Memory and TUI; one
  pending latest value per adapter slot, and capacity-one metadata channels.
  Replacement discards advisory labels, never identity or committed messages.
  Already-present chat/message labels retain their established quality; an
  uncached future presentation can fall back to its opaque ID.
- At most **256 distinct local contact reads per session**, once per JID,
  including misses/failures. This also bounds additional entries our reads can
  create in upstream `SQLStore.contactCache`; it is not a claim that all upstream
  caches/activity are globally bounded by walite. The attempt set has no eviction.
- At most **32 group network attempts per session**, one per observed group,
  including failed attempts; no retry loop. Known/live subjects skip lookups.
  Later natural subject events can still improve names after the limit.
- Each lookup gets a **three-second context deadline**, and shutdown cancels it.
  No real-client wait is in the message-admission, TUI or callback path. An
  unavailable lookup leaves a safe fallback; it cannot fail committed traffic.

These modest session limits leave headroom beyond the unchanged 16-chat/32-
message-per-chat TUI working set without mirroring the global address book.
No participant roster is traversed for name lookup. Contacts/subjects beyond
these limits still receive live metadata observations, but may remain opaque
until a later session. The cache limits are not identity-routing limits.

Explicit exclusions: **read receipts/MarkRead**, delivery-receipt changes,
selected-chat side effects, history/contact synchronization, media, roster UI,
contact editing, quote-history reconstruction and outgoing transport changes.

### Validation and manual gate

Deterministic tests cover name-source priority and Unicode/length bounds;
PN/LID mapping/order/coalescing; late saved/group updates racing stale lookups;
fixed caches, request/attempt limits and cancellation; message delivery during
a blocked group lookup; sender identity through store/LiveEvent/snapshot and
live adapters; simulated-screen chat/sender names and opaque fallbacks; same-name
distinct identities; state preservation; long-name resize and from-me rendering;
metadata-only updates, duplicate protection and redraw counts.

Manual validation must use the existing linked session (no re-pair required):
check known direct names, one actual group subject, a received group's sender
label, repeated chat switching without ghosting/PN-LID duplicates, then one
normal send and one one-to-one reply. Automated tests do not perform these real
operations. Do not mark 4C complete until this manual gate succeeds.

Automated 4C validation passed: gofmt of all changed Go files, `go mod tidy`,
`go mod tidy -diff`, `go vet ./...`, `go test ./...`, final
`go test -race ./...`, and `git diff --check`. Twenty new tests cover the
4C behavior above. Focused name/group/identity/real-text/reply/resize/ghosting
tests also pass uncached, both normally and with race detection.
`go.mod` and `go.sum` are unchanged. The first full race run encountered the
previously documented SQLite queue-saturation timing assertion (13 rather than
14 writes in its second transaction); its isolated race rerun and the subsequent
complete race suite passed. No SQLite source/tests were modified and no data
race was reported. No real connection, message send or commit was performed.

## Milestone 4C.1 incoming text-reply presentation correction

The incoming drop point was `internal/wa/whatsmeow_connection.go`:
`adaptTextMessage` extracted the extended-text body but never read its
`ContextInfo`. Outgoing/offline messages already retained `model.TextQuote`
through the store, committed LiveEvents, cmd adapters and generic TUI renderer.
Only the incoming adapter needs a production change; the in-progress
directional-layout implementation is preserved without modification.

### Confirmed upstream fields and narrow mapping

Verified against pinned whatsmeow
`v0.0.0-20260828224850-0fadda796019` and the read-only
`../references/whatsmeow` checkout (`662ad1dc6900ffe1b1a2a6bc0fca01cba488d747`):

- Repository: `go.mau.fi/whatsmeow`; licence: MPL-2.0.
- `proto/waE2E/WAWebProtobufsE2E.pb.go`: `Message.Conversation`,
  `Message.ExtendedTextMessage`, `ExtendedTextMessage.Text/ContextInfo`,
  `ContextInfo.StanzaID/Participant/QuotedMessage` and their getters.
- `store/store.go`: `Device.GetJID` and `Device.GetLID` return the linked
  account's existing PN/LID identities without a lookup.
- Adaptation: read these existing public fields/getters into walite's generic
  value. No upstream implementation or protobuf object is copied/retained.

The outer extended-text `ContextInfo.StanzaID` becomes the quoted message ID.
`QuotedMessage.Conversation` or `QuotedMessage.ExtendedTextMessage.Text` becomes
the excerpt. `model.NewTextQuote` remains the sole quote validator/owner:
valid UTF-8, existing input validation, at most 1 KiB retained on a rune boundary.
The parent message body, timestamp, direction, chat/sender IDs and group flag
are unchanged. Ordinary Conversation messages have no such quote context.

`Participant` is matched exactly against the linked account's non-device PN
and LID. Either match sets quoted `FromMe=true`, independently of the parent
message's direction. Known peer PN/LID, absent/invalid participant, unknown
participant or unavailable account identity use the existing neutral `false`;
valid quote ID/text still survives. No name-based inference, callback database
lookup, new alias entry or change to PN/LID routing is introduced.

Unsupported/media, absent text, missing/invalid stanza ID or otherwise invalid
quotes use the existing no-quote fallback: retain the incoming text message,
without an invented media label. No full quoted message snapshot is persisted.

### Shared presentation, duplicates and limitations

The path is `events.Message -> model.Event.Message.Quote -> WriteRealtime ->
Memory -> LiveEvent.Message.Quote -> cmd ReplyToID/ReplyToText/ReplyToFromMe ->
TUI -> existing ↪ renderer`. Incoming blocks remain left-aligned and outgoing
blocks right-aligned. The original quote timestamp is shown only when that
original message is already in the TUI working set; ContextInfo supplies no
quoted timestamp for this mapping, so none is invented.

Incoming group text quotes use the same generic excerpt. The current model
has only quoted ID/text/FromMe, not a quoted participant ID or display name;
group quoted-author names therefore remain unavailable. Existing current-
message sender labels/name resolution are unchanged. Group reply sending,
history reconstruction, read receipts and transport/service changes are excluded.

Existing Memory semantics retain an established quote against poorer duplicate
echoes and can enrich an initially quote-less stored message. Such duplicates
publish no second insert/LiveEvent and do not increment unread. A late quote
enrichment is available to a later snapshot, but does not retroactively refresh
an already-rendered quote-less message: no new refresh/dedup contract is added.

Deterministic coverage includes both quoted text forms, exact Unicode and 1 KiB
truncation, linked PN/LID authorship, neutral missing identity, group quotes,
unsupported/malformed fallback, the real incoming callback through Core/Memory/
LiveEvents, PN/LID duplicate retention/enrichment, and actual application/TUI
simulation frames. Screen assertions use full grapheme cells and verify a
single visible incoming reply with its quote directly above it on the left,
outgoing on the right, plain incoming unchanged, groups and timestamps on/off.
No live connection, sending or sleep-based synchronization is used.

Manual gate: receive a real direct text reply to an earlier own message and
check the visible quote/body association, left alignment and one local message;
also recheck an outgoing reply remains right-aligned. Do not commit before the
manual retest is approved.

Automated validation passed: gofmt on changed Go files, `go mod tidy`,
`go mod tidy -diff`, `go vet ./...`, `go test ./...`, `go test -race ./...`,
and `git diff --check`. Module files are unchanged. All four pre-existing
directional-layout files retain their pre-fix SHA-256 hashes. No manual WhatsApp
test or commit was performed. **Incoming replies are ready for manual test.**

## Milestone 4D.0 — SQLite committed-result compatibility

Approved prerequisite only: SQLite previously discarded insert results,
allowed an admitted caller to return cancellation before a later commit, and
could report a permission error after COMMIT. Those semantics could not replace
Memory under Core's existing B0 contract. Production still uses Memory; 4D
history/chat-list/bootstrap work has not started and 4D is not complete.

`SQLiteStore.WriteRealtime` now returns `model.LiveEventBatch`. The existing
64-request queue, one writer goroutine, 50-logical-write transaction limit and
25 ms oldest-request flush policy are unchanged. Each accepted request owns
one capacity-1 result mailbox; there are no result workers or retained commit
history. The writing transaction records INSERT outcomes and per-insert
unread/activity, then reads same-request enrichment **inside that transaction**.
Results are validated before COMMIT and released only on successful COMMIT.
Rollback discards every result in the shared transaction. No write-then-query
inference, direct LiveEvent publication or Core contract change is used.

Only new inserts publish, in request order. Incoming inserts saturating-increment
unread; outgoing/history writes do not increment it; activity is the maximum
message time. Duplicates do not re-publish or change unread/activity. They can
improve body/quote/sender fields under Memory's rules, without downgrading an
established quote. Late duplicate enrichment is available to later reads, not a
new live refresh event. SQLite's existing Unix-millisecond timestamp convention
is unchanged.

Cancellation before queue ownership can reject without writing. Once admitted,
all writer APIs wait for a definitive transaction success or rollback, including
when canceled while queued, during execution or after COMMIT. Close stops
admission and drains accepted mailboxes before joining the writer. Permission
checks run before writing and before commit; post-commit failure reports
`CacheWriteUnavailable` without changing that commit's success. Subsequent
writes must pass the same security check before proceeding. Native pruning
also separates committed work from post-commit permission degradation.

Schema version **2** is an additive transaction from v1: message group and
bounded quote fields, chat display quality, and a 128-slot advisory display-name
FIFO (matching Memory, with its next slot in app_meta). The existing sender_id
column is now used. Generic `ApplyDisplayMetadata` runs through the same writer
and `model.DisplayMetadata.Merge`; it never inserts a chat or changes activity.
Chat names/source quality, group senders, quotes and truncation markers survive
restart. Quotes retain the existing 1 KiB bound; native body pruning also clears
and accounts for quote bytes. No history, credential or session fields are added.

Core's `RetentionSnapshot`, `ApplyPrune` and `Usage` are adapters under the
existing maintenance gate. Snapshots read only metadata, in indexed descending
time/ID order, with a hard 551-summary limit and explicit overflow rejection
(never silently truncated policy input); Core's smaller configured limit still
applies. `ApplyPrune` executes the policy's bounded row-deletion/anchor plan,
atomically and idempotently, using existing checkpoint storage. Native Prune's
500-body cycle and retention rules are unchanged. Usage reuses physical
DB+WAL+SHM accounting, not Memory's logical byte estimate. Cache pressure,
checkpointing and disk-full handling remain the existing subsystem.

Deterministic tests cover Memory result/metadata parity, same-request enrichment,
shared-transaction mailbox isolation/rollback, all cancellation phases, Close,
post-commit permission degradation and the next write's rejection, v1 upgrade
and rollback, display quality/FIFO across restart, body-free snapshots, Core
pruning/anchors, usage, overflow rejection and maintenance serialization. A
fake-source Core-to-SQLite integration test proves committed-only publication;
its one-slot fake input is gated by observed commits, with no source drops.
No network or production composition is involved.

Initial focused benchmark comparison on the target Core 2 Duo T9900, linux/amd64
(`-benchtime=3x -count=1`, ms/op; short/noisy samples):

| Existing benchmark | Before | After |
| --- | ---: | ---: |
| Batch 1 / 10 / 50 | 26.49 / 38.04 / 28.26 | 40.45 / 32.80 / 20.52 |
| Single-message upsert | 26.74 | 28.50 |
| Page 100 first / middle / old | 1.14 / 2.42 / 2.83 | 1.38 / 2.27 / 3.32 |

The added fresh-insert committed-result benchmark measured 27.15 / 33.25 /
24.20 ms for 1 / 10 / 50 messages. A final-code repeat (`-benchtime=3x -count=3`)
had median existing batch times 27.41 / 30.39 / 17.47 ms, single upsert 27.08 ms,
and fresh committed-result times 26.19 / 32.25 / 31.73 ms. Page medians were
1.29 / 6.68 / 3.01 ms: middle-page samples varied to 5.22–7.97 ms in that repeat,
so these short runs are not a precise latency-regression estimate. Richer page
fields increase allocations from about 58 to 74 KiB per 100-row page; a fresh
50-message result uses about 259 KiB/op. The existing indexed page queries,
25 ms age policy, and fixed queue/batch bounds remain unchanged.

Validation passed: gofmt, `go mod tidy`, `go mod tidy -diff`, `go vet ./...`,
`go test ./...`, `go test -race ./...`, uncached focused SQLite/store/service
compatibility tests, and `git diff --check`. The integration fixture passed 20
consecutive focused race runs. An existing queue-saturation timer assertion
flaked once in an initial normal run; five unchanged focused reruns and the
final full suites passed. No timing threshold was changed. Module files are
unchanged. **4D.0 is ready for review, uncommitted; 4D remains future work.**

## Milestone 4D — Persistent chat list and bounded history bootstrap

Status: the explicitly approved one-time manual repair/re-pair succeeded. A
broad HistorySync populated the application cache, historical messages survive
restart, and the linked session reconnects without another QR. Walite does not
unlink automatically, issue history requests, or manipulate the phone.
Filtering `status@broadcast` is a separate presentation issue and is not part
of 4D. The acceptance fixes below are ready for one final manual test.

### Production cache and HistorySync boundary

Connected production now opens one SQLite application cache at
`${XDG_CACHE_HOME:-~/.cache}/walite/walite-cache.db` before constructing the
long-lived WhatsApp connection. The existing WhatsApp credential database
remains separate at `${XDG_DATA_HOME:-~/.local/share}/walite/whatsmeow-session.db`.
SQLite is the sole connected chat/message authority; Memory remains only in
offline fixtures. The cache is closed after the authenticated application and
its workers stop.

Pinned whatsmeow `INITIAL_BOOTSTRAP`, `RECENT`, `FULL`, and `ON_DEMAND`
HistorySync deliveries map respectively to walite transport-neutral initial,
recent, full, and on-demand bootstrap categories. Each upstream conversation is
converted and released independently. One fixed-capacity channel (capacity 1)
applies cancellable backpressure between the WhatsApp callback and one cache
consumer; no protobuf graph or goroutine per message is retained.

One bootstrap record owns the chat ID, bounded name/group/archive/mute/activity
metadata, the upstream unread count only when that optional field is present,
and at most the newest **50** supported text messages. Messages retain stable
ID, timestamp, direction, exact bounded Unicode text, generic quote, group
sender ID and group flag. Unsupported media/system/protocol entries are omitted
without invented body text. Metadata-only conversations still create summaries.
Messages are normalized oldest-first inside the record before the existing
historical SQLite writer receives them.

Conversation activity is the later complete Unix timestamp from
`LastMsgTimestamp` and `ConversationTimestamp`; either field may be absent in a
HistorySync variant. SQLite and TUI ordering use that absolute timestamp
descending and stable ChatID ascending for ties. Formatted `HH:MM` text is never
an ordering key, and metadata/name-only refreshes do not promote a chat.

Authoritative `PnJID`, `LidJID`, `NewJID`, and `OldJID` pairs are applied through
the existing bounded alias resolver before a bootstrap row is written. The
resolver bound is now the same 10,000-conversation application bound. On
restart, cached stable chat IDs seed that resolver before the WhatsApp
connection starts; when the opposite PN/LID arrives first, it attaches to and
resolves back to the cached primary. This prevents a persistent cache row from
splitting solely because transport identity presentation changed. No generic
store suffix rules or alias schema was added, and groups bypass direct-chat
alias slots.

Outgoing sends reuse an authoritative PN/LID alternate already learned from
HistorySync or live traffic. They do not fail merely because a redundant local
session-store lookup lacks that broad historical mapping. The established
presentation ChatID remains the committed identity, and plain/reply sends still
use the existing bounded reservation, one client, realtime SQLite commit, and
LiveEvent path.

Manual acceptance exposed a second, production-only cache shape after re-pair:
both spellings of an authoritative PN/LID pair could already exist as separate
cached rows, so startup seeded two singleton routes. The session lookup then
succeeded, but general alias resolution correctly returned
`ErrChatAliasConflict` before transport. Send routing now reconciles only that
exact case: two singleton entries plus a current authoritative opposite-server
mapping. It chooses the selected visible ChatID before `SendMessage`, removes
one bounded routing slot, and can reorient the proven pair if the other cached
row is selected later. General live alias resolution still refuses to merge
established/non-singleton relationships. No cache rows are deleted or migrated,
and echo normalization continues to target the selected committed identity.

Historical batches use `WriteHistory`, never `WriteRealtime`, and therefore do
not publish LiveEvents or increment unread once per imported message. A present
conversation unread value is authoritative, including zero, when its activity
is genuinely newer than the cached conversation. Missing metadata or an equal/
older activity snapshot preserves cached unread, so a stale bootstrap chunk
cannot resurrect a local clear. Realtime inserts after bootstrap continue
through the unchanged committed path: incoming increments from that value,
outgoing does not, and both update the same chat. Duplicate bootstrap records
remain idempotent. Completion, rather than each message, triggers one coalesced
cache-list refresh and the existing bounded cache-budget enforcement. `MarkRead`
is not called, and walite does not issue `BuildHistorySyncRequest`.

### Persistent local read state

Selecting an unread chat still clears its TUI badge immediately. The TUI now
submits that stable ChatID to the existing single cache worker instead of
blocking on SQLite. The worker owns a deterministic FIFO of at most 10,000
deduplicated chat IDs plus their visible activity watermark, uses no goroutine
per selection, and drains accepted local-read writes during shutdown.
`SQLiteStore.MarkChatLocallyRead` runs through the existing bounded single
writer and atomically updates only `chats.unread_count` to zero when cached
activity is not newer than that watermark. Thus a delayed clear cannot erase a
new message that committed first. No state file or schema migration is involved.
Memory implements the same generic cache operation for offline parity.

The ordering contract is local and transport-neutral. A new incoming realtime
insert committed after the clear increments zero to one; replaying that same
message does not increment twice; another chat is untouched. A later HistorySync
snapshot may replace unread only with newer conversation activity. Persistence
failure does not crash or undo the current TUI clear; the existing SQLite cache
failure classification remains the storage diagnostic boundary. This operation
does not call WhatsApp, send a receipt, or claim remote read state. Remote
`MarkRead` remains Milestone 4E.

### Broad summaries and lazy selected history

`ListChats` returns at most **10,000** lightweight summaries ordered by activity
descending and stable ChatID ascending. SQLite restart restores IDs, names,
group status, unread and activity without waiting for HistorySync. The bounded
bootstrap consumer preserves the first 10,000 established/cache identities and
deterministically ignores a new unknown 10,001st conversation; it never evicts
the selected/visible working set or grows storage in memory. The rendered TUI
uses the same fixed summary bound, follows selection through the full list, and
keeps the selected row visible while `j`/`k` navigate well beyond the viewport.

The TUI chat record stores only summary fields plus a nullable pointer to one
fixed 32-message buffer. Consequently 10,000 metadata-only chats allocate zero
per-chat message buffers; only chats whose page or committed live message is
present own a buffer. Scrolling the summary list does not load messages.

After the cached summary first frame is shown, the selected chat requests the
newest **32** messages using the existing indexed keyset page. One application-
owned loader has a capacity-one wake mailbox, coalesces summary refreshes, and
keeps only the newest pending selection request. It performs no query on the
TUI event loop and creates no worker per selection. Each request carries the
chat's presentation revision. A result for another selected ID or an older
revision is rejected. If a committed live message arrives while the page is in
flight, the accepted page merges rather than erases that message. Selection,
draft, cursor, reply, viewport and popup state remain presentation-owned and
transient.

An empty cache renders `No chats — syncing…`; HistorySync completion refreshes
the summary list asynchronously. Full HistorySync is never a startup gate.
On-demand older-history download is deferred to 4D.1, and read receipts remain
4E.

### Acceptance presentation fixes

Individual messages retain `HH:MM`, presented in the process local timezone.
Each visible local calendar day begins with a centered `Mon 2 Jan 2006`
separator. Separator rows participate in viewport measurement, so scrolling,
resize, unread boundaries, directionality, Unicode and reply quote rows remain
deterministic. A viewport that begins mid-day still supplies its date context.

The existing 4C display priority is unchanged. When authoritative name fields
are empty, a validated PN JID is presented as `+` plus its digits by the shared
`internal/wa` fallback. This also improves already-populated cache rows on the
next startup without exposing JID parsing to the TUI. An unmapped LID remains
its opaque stable ID until authoritative PN/name metadata arrives.

### Deterministic coverage and target measurements

Tests cover category mapping; metadata-only conversations; exact Unicode,
direction, quotes and group senders; newest-50 retention; one-slot shutdown;
PN/LID bootstrap and restart seeding; authoritative/absent unread; duplicate
replay; incoming/outgoing realtime after history; SQLite restart; deterministic
10,000/10,001 ordering; 100-chat TUI navigation; no summary message-slot
allocation; newest-32 page loading; newest-request coalescing; stale generation
rejection; live/page merge; production SQLite dependency composition; and clean
bootstrap cancellation. Local-read additions cover selection through SQLite
restart, one-chat isolation, incoming-after-clear, duplicate idempotency,
PN/LID identity stability, stale/equal versus newer HistorySync, bounded FIFO
dedup/shutdown drain, and preservation of draft/reply/viewport/popup state.
Acceptance regressions additionally cover HistorySync PN/LID plain and reply
sends through SQLite, exact single commit and echo deduplication, activity
ordering across dates/restart and insertion order, local-day separators with
scroll/realtime/unread/reply/Unicode presentation, and readable PN versus opaque
LID fallback for both new bootstrap and an already-populated cache.

Measurements on the target Intel Core 2 Duo T9900, linux/amd64
(`-benchtime=3x -count=1`, short/noisy samples):

| Operation | Time | Allocated bytes / allocations |
| --- | ---: | ---: |
| ListChats 1,000 | 11.20 ms | 569,778 B / 19,003 |
| ListChats 10,000 | 118.59 ms | 5,738,402 B / 196,699 |
| newest message page, 50 | 0.761 ms | 37,744 B / 1,047 |
| build/draw cached 10,000-summary first frame | 14.94 ms | 3,135,634 B / 14,251 |

The fixed `chatState` value is **960,024 bytes** on amd64. Its 10,000 records
contain summary fields and message-buffer pointers, not 320,000 message slots;
the benchmark's additional allocation includes owned input conversion and a
100×30 simulation screen. These measurements support the direct indexed
all-summary query and asynchronous selected-page design on the current target.

The original linked session did not replay a sufficiently broad bootstrap. The
user then explicitly completed the one-time repair/re-pair, and the expected
broad HistorySync arrived. No further unlink/re-pair is required or permitted
for this acceptance pass; the populated cache and linked session are preserved.

## Milestone 4E — Read receipts

Status: ready for automated review and manual direct/group receipt validation.
Do not mark complete until the linked-account manual test confirms the remote
sender observes the read transition.

Walite sends a remote read receipt only when `j`/`k` actually changes selection
into a chat whose local unread count is nonzero. Receipt work is not triggered
by realtime arrival, HistorySync, startup with cached unread, chat-list display,
summary/name refresh, or automatic first-chat selection. The existing local
behavior remains immediate and authoritative for walite: selection clears the
badge and asynchronously persists unread zero to SQLite independently of the
remote operation. A receipt admission/network failure neither restores that
badge nor retries forever.

The pinned whatsmeow API is:

```go
func (*Client) MarkRead(context.Context, []types.MessageID, time.Time,
    types.JID, types.JID, ...types.ReceiptType) error
```

The first JID is always the chat. The second is empty for a direct chat and is
the sending participant for a group. One call may contain several message IDs
only when they share that sender. Walite supplies the receipt worker's current
UTC time as read-at time. Direct chats reuse the connection-owned PN/LID alias
registry and session mapping; no phone number is derived outside `internal/wa`.
Group receipts are supported for cached messages whose existing SenderID is
present. Mixed-participant group frontiers are split into deterministic calls;
missing participant identity is never guessed.

Only the newest known unread incoming identities in the selected chat's current
**32-message** cache page are acknowledged. Interleaved `FromMe` messages are
excluded, message bodies are irrelevant, and no older history is loaded solely
for a receipt. If the selected page has not arrived, one transient selection
intent waits for that already-requested bounded page. This UI intent and all
receipt queues are transient and never written to SQLite.

One application-owned worker performs the synchronous network calls. It has a
fixed **32-chat** pending ring, no per-request goroutine, and no unbounded map.
Pending requests for the same ChatID coalesce into their newest/highest bounded
frontier, deduplicated by stable MessageID; if the union exceeds 32, the newest
32 are retained. A full ring rejects further remote admission without changing
local read state. Each admitted operation has a 15-second context deadline and
is attempted once. Disconnection or transport failure is controlled and causes
no background retry; a later genuinely unread selection may naturally submit a
new frontier.

Receipt tests are network-free and cover explicit selection versus background,
startup and HistorySync paths; stable IDs and timestamps; Unicode/bodyless
irrelevance; `FromMe` exclusion; deferred page loading; duplicate events and
already-read reselection; bounded request/queue behavior; coalescing; controlled
disconnection/failure; PN/LID routing; direct calls; mixed-participant groups;
SQLite local-read persistence; incoming-after-clear; and existing outgoing,
history/cache, contact/name, reply and directional-layout regressions.

Typing, presence, reactions, played/media receipts, delivery receipts, and read-
receipt status presentation are explicitly outside Milestone 4E.

### Startup alias reconciliation follow-up

The first restart after broad bootstrap exposed a pre-existing routing edge:
SQLite could legitimately contain both PN and LID rows for one direct chat, so
startup seeded two independent alias-table singletons. The first later
authoritative PN/LID live event made the general resolver return
`ErrChatAliasConflict`, stopping the service before the TUI. Read-receipt code
did not enqueue or call `MarkRead`; the restart merely exposed the cached shape.

The existing outgoing path already allowed safe reconciliation of exactly two
independently seeded singleton routes. The same narrow policy now applies when
authoritative incoming event metadata or the linked session mapping proves the
PN/LID pair: preserve the first activity-ordered cache identity, attach the
other spelling, and remove only the duplicate routing-table slot. No SQLite
chat/message row is merged, deleted, or rewritten. Any entry that already has
another relationship still returns the same controlled conflict. Regression
coverage constructs the production capabilities and worker, seeds both cached
forms, then proves the first paired live event resolves without stopping while
preserving stable message identity and Unicode.
