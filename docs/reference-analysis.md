# Reference client and whatsmeow analysis

## Scope and evidence rules

This is a source-only review of the five checked-out repositories named below. No client was run, no account was used, and no network behavior was tested. Repository status therefore means the status evidenced by the checkout; GitHub's server-side `archived` flag is **UNKNOWN** unless a checked-in file states it. Dates are commit author dates visible in each checkout. Each conclusion is explicitly classified. Paths are relative to the named repository.

## normen/whatscli

### Project status

- **CONFIRMED** — The checkout contains a relevant 2026 refresh: commit `200b0a8` (“update release”, 2026-06-02), and `go.mod` pins a 2026 whatsmeow revision. Evidence: normen/whatscli, Git `HEAD`; `go.mod`, dependency `go.mau.fi/whatsmeow`.
- **INFERENCE** — The project is maintained, because both its application release and protocol dependency were refreshed recently. A one-commit/shallow local history cannot establish release cadence. Evidence: normen/whatscli, Git `HEAD`; `go.mod`, `go.mau.fi/whatsmeow`.
- **UNKNOWN** — Whether GitHub marks the repository archived; no checked-in archive declaration exists. Evidence: normen/whatscli, repository checkout.
- **CONFIRMED** — Its protocol library remains viable in principle: it uses maintained `go.mau.fi/whatsmeow`, not an abandoned pre-multidevice implementation. Evidence: normen/whatscli, `messages/session_manager.go`, `SessionManager.getConnection`; `go.mod`, `go.mau.fi/whatsmeow`.

### Technology

- **CONFIRMED** — Go 1.25, Go modules and a small Makefile form the language/build stack; the TUI is `rivo/tview` over `tcell`. Evidence: normen/whatscli, `go.mod`, `go` directive and `github.com/rivo/tview`; `Makefile`, targets `build`, `run`; `main.go`, globals `app`, `textView`, `treeView`.
- **CONFIRMED** — WhatsApp access is through whatsmeow plus its SQLite `sqlstore`, using `mattn/go-sqlite3`. Application messages are not stored in SQLite: `MessageDatabase` is maps and slices guarded by mutexes. Evidence: normen/whatscli, `messages/session_manager.go`, `SessionManager.getConnection`; `messages/storage.go`, type `MessageDatabase`; `go.mod`.
- **CONFIRMED** — Principal supporting dependencies are XDG paths, INI configuration, terminal QR rendering, desktop notifications, clipboard and external-file opening. Evidence: normen/whatscli, `config/settings.go`, `InitConfig`; `messages/session_manager.go`, `loginWithQRCode`, `notify`; `main.go`, `safeReadClipboard`, `UiHandler.OpenFile`; `go.mod`.

### Architecture and authentication

- **CONFIRMED** — `main` constructs the UI and a single `SessionManager`; that manager owns the sole `*whatsmeow.Client`, sqlstore container, command channels and in-memory message database. Evidence: normen/whatscli, `main.go`, `main`; `messages/session_manager.go`, type `SessionManager`, `Init`, `getConnection`.
- **CONFIRMED** — First authentication obtains a QR channel, connects, prints each `code`, and returns on `success`; an existing device store connects directly. Evidence: normen/whatscli, `messages/session_manager.go`, `loginWithConnection`, `loginWithQRCode`.
- **CONFIRMED** — The manager runs in one goroutine and selects over bounded channels of capacity 10. Whatsmeow invokes `eventHandler.Handle`, which directly mutates the mutex-protected database and calls UI adapters. UI adapters generally start goroutines that call `app.QueueUpdateDraw`. Evidence: normen/whatscli, `messages/session_manager.go`, `StartManager`, `runManager`, `eventHandler.Handle`; `main.go`, methods of `UiHandler`.
- **CONFIRMED** — Protocol and UI have a narrow `UiMessageHandler` interface, but protocol coordination, message normalization, persistence access, media and commands are combined in `SessionManager`; the in-memory store also imports the whatsmeow protobuf type. Evidence: normen/whatscli, `messages/messages.go`, `UiMessageHandler`, `Message.RawMessage`; `messages/session_manager.go`, type `SessionManager`.
- **CONFIRMED** — Shutdown is flag-based, not context-based: quit enqueues `disconnect` then immediately stops tview; the manager exits only when `started` becomes false, but the disconnect command does not set it false. Most operations use `context.Background()`. Evidence: normen/whatscli, `main.go`, `handleQuit`; `messages/session_manager.go`, `runManager`, `disconnect`.
- **INFERENCE** — The manager goroutine can remain alive until process termination after UI shutdown; there is no join, channel close or cancellation contract. Evidence: normen/whatscli, `messages/session_manager.go`, `runManager`, `disconnect`; `main.go`, `handleQuit`.

### Initial and ongoing synchronization

- **CONFIRMED** — Handled whatsmeow events are `Message`, `HistorySync`, `Connected`, `Disconnected` and `LoggedOut`. Evidence: normen/whatscli, `messages/session_manager.go`, `eventHandler.Handle`.
- **CONFIRMED** — `handleHistorySync` synchronously loops over every supplied conversation and message, parses each web message, inserts it into the in-memory store, recomputes unread flags, then rebuilds chat/UI data. It does not filter by sync category, age or count. Evidence: normen/whatscli, `messages/session_manager.go`, `eventHandler.handleHistorySync`.
- **CONFIRMED** — History messages are retained without a bound for the process lifetime; per-chat slices are sorted after every new insertion, and `GetMessages` copies the full per-chat slice. Evidence: normen/whatscli, `messages/storage.go`, `MessageDatabase.AddMessage`, `GetMessages`.
- **CONFIRMED** — Duplicates are keyed globally by message ID in `messagesById`; an existing record is enriched and replaced rather than appended. Evidence: normen/whatscli, `messages/storage.go`, `MessageDatabase.AddMessage`, `replaceMessageLocked`.
- **CONFIRMED** — The TUI is started before history completes, so initial history does not gate `app.Run`; however a HistorySync handler does all parsing/store work synchronously on whatsmeow's dispatch path. Evidence: normen/whatscli, `main.go`, `main`; `messages/session_manager.go`, `eventHandler.handleHistorySync`.
- **CONFIRMED** — Older history is explicitly requested with `BuildHistorySyncRequest` using the current chat, oldest local message ID, timestamp, direction and requested count. `loadBacklog` then polls every 250 ms for up to five seconds on the manager goroutine. Evidence: normen/whatscli, `messages/session_manager.go`, `loadBacklog`; `config/settings.go`, `General.BacklogMsgQuantity`.
- **CONFIRMED** — Live and historical events share the same handler and database path; there is no priority queue. Evidence: normen/whatscli, `messages/session_manager.go`, `eventHandler.Handle`, `handleLiveMessage`, `handleHistorySync`.

### Local storage and media

- **CONFIRMED** — Whatsmeow session state is SQLite at the XDG config path returned by `GetSessionFilePath()` plus `.db`; application messages, chats and contacts exist only in memory. Evidence: normen/whatscli, `config/settings.go`, `GetSessionFilePath`; `messages/session_manager.go`, `getConnection`; `messages/storage.go`, `MessageDatabase.Init`.
- **CONFIRMED** — There are no application message tables, indexes, transactions, pruning or disk-size controls. Evidence: normen/whatscli, `messages/storage.go`, complete `MessageDatabase` implementation.
- **UNKNOWN** — Exact session database and directory modes: the application does not call `Chmod`, and effective modes depend on XDG helpers, SQLite and umask. Evidence: normen/whatscli, `config/settings.go`, `GetSessionFilePath`; `messages/session_manager.go`, `getConnection`.
- **CONFIRMED** — Media is downloaded only through explicit `download`, `open` or `show` commands, using whatsmeow `Download`; there is no history-time prefetch. Evidence: normen/whatscli, `messages/session_manager.go`, `downloadCommand`, `downloadMessage`; `main.go`, `handleMessageCommand`.

### Tests, strengths and weaknesses

- **CONFIRMED** — Tests cover in-memory insertion/unread/dedup-related behavior and download filename sanitization. No integration, network, cancellation or UI tests are present. Evidence: normen/whatscli, `messages/storage_test.go`, `TestAddMessageAndMarkChatRead`, `TestUpdateChatUnreadMarksLatestIncomingMessages`; `messages/session_manager_test.go`, `TestDownloadFileNameSanitizesPathTraversal`.
- **UNKNOWN** — No checked-in CI or race-test configuration was found. Evidence: normen/whatscli, repository file list.
- **CONFIRMED** — Dependency updating is a manual `go get -u` Make target. Evidence: normen/whatscli, `Makefile`, target `update`.
- **INFERENCE (strength)** — A single connection owner, bounded command channels, a fakeable UI interface, ID-based deduplication and opt-in media are useful walite ideas after separating responsibilities and adding cancellation. Evidence: normen/whatscli, `messages/session_manager.go`, `SessionManager`; `messages/storage.go`, `AddMessage`; `messages/messages.go`, `UiMessageHandler`.
- **CONFIRMED (weakness)** — History retention and memory are unbounded, history processing blocks its dispatch goroutine, full slices are repeatedly sorted/copied, UI calls spawn an unbounded number of goroutines, and backlog polling busy-delays manager work. Evidence: normen/whatscli, `messages/storage.go`, `AddMessage`, `GetMessages`; `messages/session_manager.go`, `handleHistorySync`, `loadBacklog`; `main.go`, methods of `UiHandler`.

### Licensing

- **CONFIRMED** — The README declares the project MIT, but this checkout contains no standalone licence file. Evidence: normen/whatscli, `README.md`, section `License`; repository file list.
- **CONFIRMED** — Current whatsmeow is MPL-2.0. Evidence: tulir/whatsmeow, `LICENSE`, heading `Mozilla Public License Version 2.0`.
- **INFERENCE** — Adapting an abstract MIT-licensed idea is low-friction; copying substantial project code requires preserving the MIT notice. Copying or modifying whatsmeow files invokes MPL-2.0 file-level source obligations. Walite should depend on upstream whatsmeow rather than copy it. Evidence: normen/whatscli, `README.md`, `License`; tulir/whatsmeow, `LICENSE`, sections 1.10 and 3.
- **UNKNOWN** — Licence completeness for every transitive dependency cannot be established from `go.mod`; no bundled third-party notice exists in this checkout. Evidence: normen/whatscli, `go.mod`.

## vicentereig/whatsapp-cli

### Project status

- **CONFIRMED** — The latest checkout change fixes store-path ambiguity and restricts sensitive directories, dated 2026-07-21; `go.mod` pins a 2026-07-20 whatsmeow revision and CI runs tests on pushes/PRs. Evidence: vicentereig/whatsapp-cli, Git `HEAD`; `go.mod`; `.github/workflows/ci.yml`, job `tests`.
- **INFERENCE** — The project is actively maintained. The local shallow history prevents a cadence analysis. Evidence: vicentereig/whatsapp-cli, Git `HEAD`; `.github/workflows/ci.yml`.
- **UNKNOWN** — GitHub archive status is not encoded locally. Evidence: vicentereig/whatsapp-cli, repository checkout.
- **CONFIRMED** — Its underlying library is current whatsmeow and remains viable, although the changelog records that an update was needed for a 405 “client outdated” failure. Evidence: vicentereig/whatsapp-cli, `go.mod`; `CHANGELOG.md`, `v1.3.3`.

### Technology and architecture

- **CONFIRMED** — This is a Go 1.25, Go-modules, manual `flag`-parsed JSON CLI, not a TUI. Principal dependencies are whatsmeow, `mattn/go-sqlite3`, `mdp/qrterminal`, protobuf and testify. Evidence: vicentereig/whatsapp-cli, `go.mod`; `main.go`, `main`, `usage`; `internal/output/output.go`, `Success`, `Error`.
- **CONFIRMED** — `App` coordinates injected `WAClient` and `MessageStore` interfaces; production implementations wrap one whatsmeow client and one application SQLite database. Evidence: vicentereig/whatsapp-cli, `internal/commands/commands.go`, type `App`, `NewApp`, `NewAppWithDeps`; `internal/commands/interfaces.go`, `WAClient`, `MessageStore`.
- **CONFIRMED** — Authentication uses QR pairing when `Store.ID` is nil; subsequent commands connect with the stored device. The `WAClient` owns the protocol connection. Evidence: vicentereig/whatsapp-cli, `internal/client/client.go`, `NewWAClient`, `Authenticate`, `Connect`.
- **CONFIRMED** — `sync` registers one event callback, connects, and waits on `ctx.Done()`. `main` supplies signal cancellation to sync and a five-minute timeout to other commands; `defer app.Close()` stops media workers, disconnects and closes the store. Evidence: vicentereig/whatsapp-cli, `main.go`, `main`; `internal/client/client.go`, `StartSync`; `internal/commands/commands.go`, `App.Sync`, `App.Close`.
- **CONFIRMED** — Protocol, orchestration and persistence are separated by interfaces, but event callbacks directly perform name lookup and individual SQLite writes. Evidence: vicentereig/whatsapp-cli, `internal/commands/commands.go`, `App.Sync`; `internal/store/store.go`, `StoreChat`, `StoreMessage`.

### Synchronization

- **CONFIRMED** — `Sync` handles `Message`, `HistorySync`, `Connected` and `Disconnected`. Real-time messages and all conversations/messages in every HistorySync are stored synchronously in the callback. No sync-category, age, count or size filter exists. Evidence: vicentereig/whatsapp-cli, `internal/commands/commands.go`, `App.Sync`, local `eventHandler`.
- **CONFIRMED** — Every supplied history message body and media key/path metadata is retained in `messages.db`; uniqueness is `(id, chat_jid)` with an upsert, so repeats update rather than duplicate. Evidence: vicentereig/whatsapp-cli, `internal/store/store.go`, schema in `NewMessageStore`, `StoreMessage`.
- **CONFIRMED** — There is no application bulk message slice: messages stream through the history loop into SQLite one by one. The decoded HistorySync protobuf itself is necessarily resident while the callback iterates it. Evidence: vicentereig/whatsapp-cli, `internal/commands/commands.go`, `App.Sync`; tulir/whatsmeow, `types/events/events.go`, type `HistorySync`.
- **CONFIRMED** — There is no UI and no older-history-on-demand command. `sync` remains occupied until cancellation. Real-time and historical events have no separate application queues or priority. Evidence: vicentereig/whatsapp-cli, `main.go`, `usage`; `internal/commands/commands.go`, `App.Sync`.
- **CONFIRMED** — Media is automatically prefetched during both real-time and history sync whenever direct path and key exist. Four workers consume a nominally bounded channel of 16, but overflow starts a goroutine that blocks until it can enqueue; therefore pending goroutines are unbounded. Evidence: vicentereig/whatsapp-cli, `internal/commands/commands.go`, `newMediaDownloadWorker`, `mediaDownloadWorker.Enqueue`, `App.Sync`.

### Local storage

- **CONFIRMED** — Default storage is CWD-relative `./store`; `--store` overrides it. Session state is `whatsapp.db` via whatsmeow sqlstore; application data is `messages.db`; downloads default below `store/media/<chat>/<message>/<type>`. Evidence: vicentereig/whatsapp-cli, `main.go`, `extractGlobalFlags`; `internal/client/client.go`, `NewWAClient`; `internal/commands/commands.go`, `NewApp`, `resolveOutputPath`.
- **CONFIRMED** — `messages.db` has `chats(jid PRIMARY KEY, name, last_message_time)` and `messages` with content/media metadata, composite primary key `(id, chat_jid)` and a foreign key. No secondary indexes are created. Evidence: vicentereig/whatsapp-cli, `internal/store/store.go`, `NewMessageStore`.
- **CONFIRMED** — Chat and message writes are separate `Exec` calls with upserts, not explicit transactions or batches. No cache pruning, age limit or disk quota exists. Evidence: vicentereig/whatsapp-cli, `internal/store/store.go`, `StoreChat`, `StoreMessage`; `internal/commands/commands.go`, `App.Sync`.
- **CONFIRMED** — Store/media directories are requested as 0700 and downloaded files are explicitly chmodded 0600 after an atomic temporary-file rename. Existing overly-permissive directories are not tightened by `MkdirAll`. Evidence: vicentereig/whatsapp-cli, `internal/client/client.go`, `NewWAClient`, `DownloadMediaToFile`; `internal/store/store.go`, `NewMessageStore`; `internal/commands/commands.go`, `downloadMediaAndPersist`.
- **UNKNOWN** — Final SQLite file modes are not explicitly set and remain umask/driver dependent, despite directory protection. Evidence: vicentereig/whatsapp-cli, `internal/client/client.go`, `NewWAClient`; `internal/store/store.go`, `NewMessageStore`.

### Tests, strengths and weaknesses

- **CONFIRMED** — Unit/behavioral tests cover CLI parsing/store presence, output, store queries/upserts/media metadata, name/message handling and injected command behavior. Tests use fakes and temporary SQLite stores; no live WhatsApp integration is required. Evidence: vicentereig/whatsapp-cli, `main_test.go`; `store_presence_test.go`; `internal/client/*_test.go`; `internal/commands/*_test.go`; `internal/store/store_test.go`.
- **CONFIRMED** — CI runs `go test ./...`, but no race, load, cancellation or HistorySync stress test is configured. Evidence: vicentereig/whatsapp-cli, `.github/workflows/ci.yml`, step `Go test`; test file inventory.
- **INFERENCE (strength)** — Injected protocol/store interfaces, context-owned sync lifetime, conflict-key deduplication and atomic opt-in download code are good patterns for walite. Evidence: vicentereig/whatsapp-cli, `internal/commands/interfaces.go`; `main.go`, `main`; `internal/store/store.go`, `StoreMessage`; `internal/client/client.go`, `DownloadMediaToFile`.
- **CONFIRMED (weakness)** — Event callbacks do slow database and contact/group lookup work, history writes are unbatched, cache growth is unlimited, message-search lacks supporting indexes, and automatic history-media prefetch can create unlimited waiting goroutines and large disk use. Evidence: vicentereig/whatsapp-cli, `internal/commands/commands.go`, `App.Sync`, `mediaDownloadWorker.Enqueue`; `internal/store/store.go`, `NewMessageStore`, `ListMessages`.

### Licensing

- **CONFIRMED** — README metadata says MIT, but no standalone `LICENSE` is present in this checkout. Evidence: vicentereig/whatsapp-cli, `README.md`, header metadata; repository file list.
- **CONFIRMED** — Relevant whatsmeow is MPL-2.0. Evidence: tulir/whatsmeow, `LICENSE`.
- **INFERENCE** — Copying substantial project code should preserve its MIT notice, although the missing licence file warrants confirmation; modifying copied whatsmeow files would trigger MPL-2.0 file-level obligations. Prefer independent implementation against upstream APIs. Evidence: vicentereig/whatsapp-cli, `README.md`, `License`; tulir/whatsmeow, `LICENSE`, section 3.
- **UNKNOWN** — The complete licence posture of all Go dependencies is not documented in a bundled notice. Evidence: vicentereig/whatsapp-cli, `go.mod`.

## dimaskiddo/go-whatsapp-cli

### Project status and technology

- **CONFIRMED** — The latest checkout change is a Dockerfile update dated 2023-07-31; the hard-coded web version is `2.2147.16`, and the protocol dependency is the 2021 `Rhymen/go-whatsapp` revision. Evidence: dimaskiddo/go-whatsapp-cli, Git `HEAD`; `internal/cmd/whatsapp.go`, constants `WhatsAppVerMajor/Minor/Build`; `go.mod`.
- **INFERENCE** — The client is inactive as a WhatsApp client: its last relevant protocol/application code is tied to the retired pre-multidevice web stack, while the latest visible change is packaging-only. This conclusion is based on code viability, not age alone. Evidence: dimaskiddo/go-whatsapp-cli, `pkg/whatsapp/whatsapp.go`, `WASessionInit`, `WASyncVersion`; `go.mod`, `github.com/Rhymen/go-whatsapp`; Git `HEAD`.
- **UNKNOWN** — Server-side GitHub archived status is not available locally. Evidence: dimaskiddo/go-whatsapp-cli, repository checkout.
- **CONFIRMED** — The underlying library is not viable for current walite: it is not whatsmeow and uses the old WhatsApp Web connection/session API. Evidence: dimaskiddo/go-whatsapp-cli, `pkg/whatsapp/whatsapp.go`, imports and `WASessionLogin`; `go.mod`.
- **CONFIRMED** — The project is Go 1.19, Cobra, Make and GoReleaser/Docker oriented; it has no TUI or local message database. Principal dependencies are `Rhymen/go-whatsapp`, Cobra, gabs JSON, logrus and terminal QR. Evidence: dimaskiddo/go-whatsapp-cli, `go.mod`; `Makefile`; `.goreleaser.yml`; `pkg/whatsapp/whatsapp.go`.

### Architecture, synchronization and storage

- **CONFIRMED** — Login creates a connection, prints QR from an unbuffered channel in a helper goroutine, gob-encodes the returned session, tests connectivity, and disconnects. The daemon later restores the gob session and assigns the global `WAConn`. Evidence: dimaskiddo/go-whatsapp-cli, `pkg/whatsapp/whatsapp.go`, `WASessionLogin`, `WASessionSave`, `WASessionRestore`; `internal/cmd/daemon.go`, command `Daemon`.
- **CONFIRMED** — The daemon owns a global connection and global send mutex. A library callback object `WAHandler` processes errors and text messages; accepted text commands invoke a JSON-configured local command and send replies. Evidence: dimaskiddo/go-whatsapp-cli, `pkg/whatsapp/whatsapp.go`, globals `WAConn`, `WACMutex`, type `WAHandler`, `HandleTextMessage`, `WASendWithMutex`.
- **CONFIRMED** — Its concurrency model uses callback handlers, a signal channel, a perpetual five-second polling loop, delayed reconnect and a mutex that deliberately sleeps 1–3 seconds while serializing sends. Cancellation is process/signal based; the command calls `os.Exit(0)`. Evidence: dimaskiddo/go-whatsapp-cli, `internal/cmd/daemon.go`, command `Daemon`; `pkg/whatsapp/whatsapp.go`, `HandleError`, `WASendWithMutex`.
- **CONFIRMED** — There is no whatsmeow event handling, `HistorySync`, retained message cache, older-history request, duplicate handling or live-versus-history priority. Evidence: dimaskiddo/go-whatsapp-cli, complete `pkg/whatsapp/whatsapp.go`; repository-wide absence of `HistorySync` and whatsmeow imports.
- **CONFIRMED** — Session storage is the fixed CWD-relative `./config/stores/session.gob`; it is written with `os.Create`. There are no tables, indexes, transactions, cache pruning or media downloads. Evidence: dimaskiddo/go-whatsapp-cli, `internal/cmd/login.go`, variable `file`; `internal/cmd/daemon.go`, variable `file`; `pkg/whatsapp/whatsapp.go`, `WASessionSave`.
- **UNKNOWN** — Session file/directory permissions depend on umask and pre-existing directories; no explicit mode hardening is present. Evidence: dimaskiddo/go-whatsapp-cli, `pkg/whatsapp/whatsapp.go`, `WASessionSave`.

### Tests, strengths, weaknesses and licensing

- **CONFIRMED** — The README states tests are not ready, and no project test files or CI workflow are present. Dependency updating is manual vendoring through Make targets. Evidence: dimaskiddo/go-whatsapp-cli, `README.md`, `Running The Tests`; `Makefile`, targets `vendor`, `pull`.
- **INFERENCE (strength)** — Explicitly separating login and long-running daemon commands and serializing access to a connection are general ideas, but their concrete implementation is unsuitable for walite. Evidence: dimaskiddo/go-whatsapp-cli, `internal/cmd/login.go`, `Login`; `internal/cmd/daemon.go`, `Daemon`; `pkg/whatsapp/whatsapp.go`, `WASendWithMutex`.
- **CONFIRMED (weakness)** — Global mutable connection state, polling, `context`-free sleeps, process exit for shutdown, manual client-version pinning, insecurely unspecified session mode and command-triggered automatic replies conflict with walite's concurrency, security and human-send requirements. Evidence: dimaskiddo/go-whatsapp-cli, `pkg/whatsapp/whatsapp.go`, globals, `WASyncVersion`, `HandleTextMessage`; `internal/cmd/daemon.go`, `Daemon`.
- **UNKNOWN** — No project licence file or licence declaration was found; therefore copying/adapting project code has no established permission in this checkout. Evidence: dimaskiddo/go-whatsapp-cli, repository file list and `README.md`.
- **UNKNOWN** — The exact licence of the pinned Rhymen/go-whatsapp revision is not bundled here; `go.mod` alone does not establish it. Evidence: dimaskiddo/go-whatsapp-cli, `go.mod`.
- **CONFIRMED** — This project does not use whatsmeow, so the current whatsmeow MPL-2.0 licence is not its protocol-library licence. Evidence: dimaskiddo/go-whatsapp-cli, `go.mod`; tulir/whatsmeow, `LICENSE`.

## d99kris/nchat

### Project status and technology

- **CONFIRMED** — The checkout has active relevant maintenance through 2026-08-02, including adjustment of Go dependency version checks; CMake validates a minimum Go version and conditionally builds WhatsApp. Evidence: d99kris/nchat, Git `HEAD`; `CMakeLists.txt`, sections `Optional component - WhatsApp`, `Check Golang version`.
- **INFERENCE** — nchat is actively maintained. Evidence: d99kris/nchat, Git `HEAD`; `.github/workflows/linux.yml`, `.github/workflows/macos.yml`.
- **UNKNOWN** — GitHub archive status is not encoded locally. Evidence: d99kris/nchat, repository checkout.
- **CONFIRMED** — Its WhatsApp layer uses a vendored and locally patched current whatsmeow tree; the Go module replaces upstream with `./ext/whatsmeow`. This is viable but increases update/patch maintenance. Evidence: d99kris/nchat, `lib/wmchat/go/go.mod`, `replace`; `lib/wmchat/go/ext/nchat-whatsmeow.patch`; `utils/whatsmeow-update`.
- **CONFIRMED** — nchat is C++14/CMake with ncurses UI, pthreads, SQLite and protocol modules; WhatsApp is a Go/cgo static/shared bridge (`wmchat`) around whatsmeow and go-sqlite3. Evidence: d99kris/nchat, `CMakeLists.txt`, `project`, source list and `target_link_libraries`; `lib/wmchat/CMakeLists.txt`; `lib/wmchat/go/go.mod`.

### Architecture and authentication

- **CONFIRMED** — The common `Protocol` interface separates UI from each service. `WmChat` owns a worker thread and request deque; the Go bridge owns indexed whatsmeow clients/handlers protected by a mutex. Notifications cross Go/C through `CWmNew*Notify`, enter `WmChat`, then pass through `MessageCache` to the UI model. Evidence: d99kris/nchat, `lib/common/src/protocol.h`, class `Protocol`; `lib/wmchat/src/wmchat.cpp`, `WmChat::Process`, `SendRequest`, `CallMessageHandler`; `lib/wmchat/go/gowm.go`, `AddConn`, `WmEventHandler.HandleEvent`; `lib/wmchat/go/cgowm.go`, `CWmNewMessagesNotify`.
- **CONFIRMED** — Setup creates a profile, initializes whatsmeow/sqlstore, starts QR/passkey login and keeps session state in the profile. Loaded profiles reconnect without UI owning the connection. Evidence: d99kris/nchat, `lib/wmchat/src/wmchat.cpp`, `WmChat::SetupProfile`, `LoadProfile`, `Login`; `lib/wmchat/go/gowm.go`, `WmInit`, `WmLogin`.
- **CONFIRMED** — Shutdown asks the Go client to disconnect/cleanup, marks the C++ protocol loop stopped, signals its condition variable and joins its thread. MessageCache similarly signals and joins its own worker. Evidence: d99kris/nchat, `lib/wmchat/src/wmchat.cpp`, `WmChat::Logout`, `CloseProfile`; `lib/ncutil/src/messagecache.cpp`, `MessageCache::Cleanup`.
- **CONFIRMED** — The protocol and cache request deques have mutex/condition-variable coordination but no capacity bound or backpressure. Evidence: d99kris/nchat, `lib/wmchat/src/wmchat.cpp`, `WmChat::SendRequest`; `lib/wmchat/src/wmchat.h`, `m_RequestsQueue`; `lib/ncutil/src/messagecache.cpp`, `m_Queue`, `EnqueueRequest`.

### Synchronization

- **CONFIRMED** — The WhatsApp handler covers app-state completion, push names, connection state, messages, undecryptable messages, receipts, presence, chat presence, HistorySync, app state, logout, QR/pairing, joined groups, offline-sync completion, group info, chat mutations and client-outdated events. Evidence: d99kris/nchat, `lib/wmchat/go/gowm.go`, `WmEventHandler.HandleEvent`.
- **CONFIRMED** — `HandleHistorySync` synchronously iterates every supplied conversation/message and sends each through the same `HandleMessage` path, then emits chat state. It does not filter categories, age or count. Evidence: d99kris/nchat, `lib/wmchat/go/gowm.go`, `WmEventHandler.HandleHistorySync`, `HandleMessage`.
- **CONFIRMED** — History is persisted through C callbacks and the asynchronous `MessageCache`; the decoded batch is in memory during handler execution. Duplicate persistence is constrained by the cache schema/key and sequence logic rather than a distinct history/live priority mechanism. Evidence: d99kris/nchat, `lib/wmchat/go/gowm.go`, `HandleHistorySync`; `lib/wmchat/src/wmchat.cpp`, `CallMessageHandler`; `lib/ncutil/src/messagecache.cpp`, `AddMessages`, `PerformAddMessages`.
- **CONFIRMED** — The UI starts independently of HistorySync and exposes paged history requests. `UiModel::RequestMessages` computes a bounded `limit`; `WmChat::PerformRequest` first asks `MessageCache::FetchMessagesFrom`. Evidence: d99kris/nchat, `src/uimodel.cpp`, `UiModel::Impl::RequestMessages`; `lib/wmchat/src/wmchat.cpp`, `GetMessagesRequestType`; `lib/ncutil/src/messagecache.cpp`, `FetchMessagesFrom`.
- **UNKNOWN** — The reviewed WhatsApp bridge does not call current `BuildHistorySyncRequest`; whether missing older WhatsApp messages can be fetched from the phone beyond already cached/initially supplied data is not established. Evidence: d99kris/nchat, `lib/wmchat/go/gowm.go`, `WmGetMessages`; repository-wide absence of `BuildHistorySyncRequest` outside vendored whatsmeow.
- **CONFIRMED** — Live and historical messages share whatsmeow's synchronous handler and the same callback/cache queues; no higher-priority real-time lane exists. Evidence: d99kris/nchat, `lib/wmchat/go/gowm.go`, `WmEventHandler.HandleEvent`, `HandleHistorySync`; `lib/ncutil/src/messagecache.cpp`, `m_Queue`.

### Local storage and media

- **CONFIRMED** — whatsmeow session state uses SQLite below the WhatsApp profile; contacts/senders also have serialized map files. Application history uses one SQLite cache per profile below the application history directory. Evidence: d99kris/nchat, `lib/wmchat/go/gowm.go`, `WmInit`, `GetContactsStorePath`, `GetSendersStorePath`; `lib/ncutil/src/messagecache.cpp`, `MessageCache::Init`, `AddProfile`.
- **CONFIRMED** — The cache defines contacts, chats, messages and group-member tables with indexes/unique constraints in `MessageCache::AddProfile`; writes are serialized by a cache worker and DB mutex. Evidence: d99kris/nchat, `lib/ncutil/src/messagecache.cpp`, `MessageCache::AddProfile`, table constants, `Process`, `PerformRequest`.
- **CONFIRMED** — Request queues are unbounded, and no age, message-count or byte-size pruning policy was found for the message cache. Evidence: d99kris/nchat, `lib/ncutil/src/messagecache.cpp`, `m_Queue`, `EnqueueRequest`; repository search for pruning in `MessageCache`.
- **CONFIRMED** — Attachments are represented as not downloaded and fetched through explicit UI open/save actions; history parsing builds download metadata but does not fetch bytes. Evidence: d99kris/nchat, `lib/wmchat/go/gowm.go`, `DownloadableMessageToFileId`, `WmDownloadFile`, `HandleMessage`; `src/uimodel.cpp`, `GetMessageAttachmentPath`, `DownloadAttachment`.
- **CONFIRMED** — Profile setup calls `mkdir(..., 0777)` and relies on umask; exact private file modes are not enforced at that boundary. Evidence: d99kris/nchat, `lib/wmchat/src/wmchat.cpp`, `WmChat::SetupProfile`.

### Tests, strengths and weaknesses

- **CONFIRMED** — Project workflows build/test multiple platforms, and `doc/TESTING.md` documents manual/automated testing facilities; bundled whatsmeow has its upstream tests. No focused nchat WhatsApp HistorySync, queue-bound or race suite was found. Evidence: d99kris/nchat, `.github/workflows/linux.yml`, `.github/workflows/macos.yml`; `doc/TESTING.md`; `lib/wmchat/go/ext/whatsmeow/client_test.go`.
- **CONFIRMED** — whatsmeow updates are handled by a dedicated update/patch workflow and vendored third-party licence inventory. Evidence: d99kris/nchat, `utils/whatsmeow-update`; `utils/whatsmeow-mkpatch`; `lib/wmchat/THIRD_PARTY_LICENSES`.
- **INFERENCE (strength)** — The protocol interface, independent UI startup, cache worker, paginated queries, explicit media retrieval and joinable thread shutdown are useful walite concepts. Evidence: d99kris/nchat, `lib/common/src/protocol.h`, `Protocol`; `lib/ncutil/src/messagecache.cpp`, `Process`; `src/uimodel.cpp`, `RequestMessages`; `lib/wmchat/src/wmchat.cpp`, `Logout`.
- **CONFIRMED (weakness)** — Both principal deques are unbounded; HistorySync is handled synchronously with per-message cross-language callbacks; disk retention is not bounded; profile permissions are not explicitly private; and vendoring/patching whatsmeow raises update and licensing overhead. Evidence: d99kris/nchat, `lib/wmchat/src/wmchat.cpp`, `SendRequest`; `lib/ncutil/src/messagecache.cpp`, `EnqueueRequest`; `lib/wmchat/go/gowm.go`, `HandleHistorySync`; `lib/wmchat/go/go.mod`, `replace`; `lib/wmchat/src/wmchat.cpp`, `SetupProfile`.

### Licensing

- **CONFIRMED** — Core nchat and wmchat source headers state MIT, and the root `LICENSE` contains MIT. Build output becomes GPLv3 when WhatsApp is linked because whatsmeow links GPL-3.0 libsignal; Signal-enabled output is AGPLv3. Evidence: d99kris/nchat, `LICENSE`; `lib/wmchat/src/wmchat.cpp`, header; `CMakeLists.txt`, target licence section near `NCHAT_LICENSE`.
- **CONFIRMED** — The vendored whatsmeow source is MPL-2.0 and its relevant libsignal dependency is GPL-3.0; third-party notices enumerate bundled components. Evidence: d99kris/nchat, `lib/wmchat/go/ext/whatsmeow/LICENSE`; `lib/wmchat/THIRD_PARTY_LICENSES`; `CMakeLists.txt`, licence-selection comments.
- **INFERENCE** — Copying core MIT ideas/code requires its notice; copying nchat's patched whatsmeow files adds MPL file-level duties, and distributing a linked derivative that incorporates its GPL libsignal arrangement may impose GPLv3 terms. Walite should use upstream modules and independently implement architectural patterns. Evidence: d99kris/nchat, `LICENSE`; `CMakeLists.txt`, licence selection; `lib/wmchat/go/ext/nchat-whatsmeow.patch`.
- **UNKNOWN** — Exact obligations for every optional/static bundled dependency vary by build flags and require a release-specific licence audit. Evidence: d99kris/nchat, `utils/dist/THIRD_PARTY_LICENSES.static`; `CMakeLists.txt`, feature options.

## Current tulir/whatsmeow analysis

The answers below apply to checkout `662ad1d` dated 2026-07-30.

1. **UNKNOWN — Can a companion disable initial history transfer?** `DeviceProps.RequireFullSync` defaults false, but that name does not say “disable all history,” and source contains no supported boolean documented with that guarantee. `ManualHistorySyncDownload` only changes application-side downloading/receipts after notification. Evidence: tulir/whatsmeow, `store/clientpayload.go`, `DeviceProps`; `client.go`, fields `ManualHistorySyncDownload`, `DisableManualHistorySyncReceipt`; `message.go`, `handleProtocolMessage`.
2. **UNKNOWN — Can it request only a fixed number of recent messages initially?** The pairing protobuf has `initialSyncMaxMessagesPerChat`, but whatsmeow leaves it nil and source does not document server semantics or guarantee enforcement. This is distinct from on-demand `count`. Evidence: tulir/whatsmeow, `proto/waCompanionReg/WACompanionReg.proto`, `DeviceProps.HistorySyncConfig.initialSyncMaxMessagesPerChat`; `store/clientpayload.go`, `DeviceProps`.
3. **UNKNOWN — Can it configure the types of initial HistorySync data sent?** Capability flags exist for call logs, bots, groups, inline contacts and other features, but source provides no supported category-selection API or guarantee that flags suppress categories. Evidence: tulir/whatsmeow, `proto/waCompanionReg/WACompanionReg.proto`, `HistorySyncConfig`; `store/clientpayload.go`, `DeviceProps`.
4. **CONFIRMED — Categories are `INITIAL_BOOTSTRAP`, `INITIAL_STATUS_V3`, `FULL`, `RECENT`, `PUSH_NAME`, `NON_BLOCKING_DATA` and `ON_DEMAND`.** Evidence: tulir/whatsmeow, `proto/waHistorySync/WAWebProtobufsHistorySync.proto`, enum `HistorySync.HistorySyncType`.
5. **CONFIRMED — Conversation entries carry chat metadata and messages; `PUSH_NAME` carries push-name contacts; inline contacts and PN/LID mappings are top-level optional data.** Source does not provide a strict one-category-to-payload guarantee, so the exact category containing every metadata/contact field is **UNKNOWN**. Evidence: tulir/whatsmeow, `proto/waHistorySync/WAWebProtobufsHistorySync.proto`, messages `HistorySync`, `Conversation`, `Pushname`, `InlineContact`.
6. **CONFIRMED — An application may decline to retain old bodies in its own database while keeping the whatsmeow session database.** whatsmeow dispatches parsed history but does not automatically persist ordinary message bodies to sqlstore. It independently stores NCT salt, PN/LID mappings, push names, message secrets/privacy tokens and global settings in `DownloadHistorySync`. Evidence: tulir/whatsmeow, `message.go`, `DownloadHistorySync`, `storeHistoricalMessageSecrets`, `storeGlobalSettings`, `storeHistoricalPNLIDMappings`; `types/events/events.go`, `HistorySync`.
7. **CONFIRMED — Merely ignoring the application `HistorySync` event does not undo whatsmeow cryptographic/session storage, because internal storage is scheduled before the event is returned/dispatched.** **INFERENCE** — Setting manual download and never calling `DownloadHistorySync` may skip message secrets, tokens/mappings/settings contained in that blob and is therefore not equivalent or recommended. Evidence: tulir/whatsmeow, `message.go`, `handleHistorySyncNotificationLoop`, `DownloadHistorySync`; `client.go`, `ManualHistorySyncDownload`.
8. **CONFIRMED — Within one `dispatchEvent` call, registered handlers run synchronously and sequentially under an `RLock`.** Node handling itself uses a queue and goroutines; history notifications are consumed by a dedicated goroutine, which synchronously dispatches each downloaded history event. Different library goroutines can therefore dispatch different events concurrently; global serialization across all events is **UNKNOWN/not guaranteed**. Evidence: tulir/whatsmeow, `client.go`, `dispatchEvent`, `handlerQueueLoop`; `message.go`, `handleHistorySyncNotificationLoop`; `keepalive.go`, goroutine calls to `dispatchEvent`.
9. **CONFIRMED — Handlers should not remove handlers directly (deadlock warning).** **INFERENCE** — They should also avoid database batches, bulk parsing, blocking UI redraws, media I/O and blocking sends because synchronous dispatch holds the handler-list read lock and delays the source goroutine. Evidence: tulir/whatsmeow, `client.go`, `RemoveEventHandler`, `dispatchEvent`; `message.go`, `handleHistorySyncNotificationLoop`.
10. **CONFIRMED — An application can enqueue live `Message` events into a higher-priority bounded queue than HistorySync work.** whatsmeow exposes the events independently and imposes no application queue policy. Correctness requires making the callback enqueue short/nonblocking and defining overflow/backpressure. Evidence: tulir/whatsmeow, `types/events/events.go`, types `Message`, `HistorySync`; `client.go`, `dispatchEvent`.
11. **CONFIRMED — An older-history request needs the chat JID, oldest known message ID, whether it was from self, its timestamp, and a requested count.** `BuildHistorySyncRequest` derives these from `types.MessageInfo`; its comment recommends 50. Sender is not serialized by the current builder. Evidence: tulir/whatsmeow, `send.go`, `Client.BuildHistorySyncRequest`; `types/message.go`, `MessageInfo`.
12. **UNKNOWN — WhatsApp does not provide a success guarantee in source.** The request comment says the response “will come” as ON_DEMAND, but the protobuf also models “more messages ... but no access,” and no retry/availability contract is implemented. Treat response, count and availability as best effort. Evidence: tulir/whatsmeow, `send.go`, `BuildHistorySyncRequest`; `proto/waHistorySync/WAWebProtobufsHistorySync.proto`, `Conversation.EndOfHistoryTransferType`.
13. **CONFIRMED — Old `Rhymen/go-whatsapp` APIs (`NewConn`, `Login`, `Restore`, handler interfaces and manual client versions) used by dimaskiddo are unrelated/obsolete for whatsmeow.** **CONFIRMED** — In current whatsmeow, the generic `DownloadAny` helper is deprecated in favor of selecting a concrete downloadable message and `Download`; `GenerateMessageIDLegacy` and `RevokeMessage` are deprecated in favor of `GenerateMessageID` and `BuildRevoke`. **CONFIRMED** — Current media-to-file APIs have changed, as vicentereig's source notes removal of `fileLength` and addition of `allowNoHash`. Evidence: dimaskiddo/go-whatsapp-cli, `pkg/whatsapp/whatsapp.go`, `WASessionInit`, `WASessionLogin`; tulir/whatsmeow, `download.go`, `DownloadAny`; `send.go`, `GenerateMessageIDLegacy`, `RevokeMessage`; vicentereig/whatsapp-cli, `internal/client/client.go`, `DownloadMediaToFile`.
14. **UNKNOWN — Source does not establish server-side meanings/enforcement for every `HistorySyncConfig` flag, a way to disable initial transfer, an initial exact message limit, category delivery order, maximum blob size, global event ordering, on-demand success/completeness, or the consequences of suppressing receipts.** Evidence: tulir/whatsmeow, `proto/waCompanionReg/WACompanionReg.proto`, `HistorySyncConfig`; `client.go`, `ManualHistorySyncDownload`; `send.go`, `BuildHistorySyncRequest`.

## Comparison table

| Project | Status | UI / language | Protocol | Application storage | History behavior | Queue/cancellation | Media |
|---|---|---|---|---|---|---|---|
| normen/whatscli | **INFERENCE:** maintained | tview/tcell, Go | current whatsmeow | unbounded in-memory maps/slices | all supplied messages synchronously retained | channels bounded 10; shutdown not context-owned | explicit only |
| vicentereig/whatsapp-cli | **INFERENCE:** active | JSON CLI, Go | current whatsmeow | SQLite, no pruning/indexes beyond PK | all supplied messages synchronously upserted | sync context-owned; media overflow creates unbounded goroutines | automatic prefetch during sync plus explicit command |
| dimaskiddo/go-whatsapp-cli | **INFERENCE:** inactive/unviable | command/daemon, Go | obsolete Rhymen/go-whatsapp | session gob only | no HistorySync support | polling/sleeps/global mutex; signal + `os.Exit` | none |
| d99kris/nchat | **INFERENCE:** active | ncurses, C++/Go | patched vendored current whatsmeow | SQLite cache plus session SQLite; no size pruning | all supplied messages synchronously bridged to async cache | joinable workers but unbounded deques | explicit only |

Table evidence: the corresponding project sections and cited symbols above. **UNKNOWN:** none of the local checkouts establishes GitHub's archive flag.

## Lessons suitable for walite

- **INFERENCE** — Adopt vicentereig's dependency-injected protocol/store seams, nchat's independently available UI and paginated cache reads, whatscli's opt-in media behavior, and ID-based SQLite upserts. Reimplement rather than copy. Evidence: vicentereig/whatsapp-cli, `internal/commands/interfaces.go`; d99kris/nchat, `lib/common/src/protocol.h`, `src/uimodel.cpp::RequestMessages`; normen/whatscli, `downloadCommand`; vicentereig/whatsapp-cli, `StoreMessage`.
- **INFERENCE** — Keep event callbacks to classification plus bounded enqueue. Use separate live and history queues, always service live first, and document what happens when either fills. Evidence: tulir/whatsmeow, `client.go`, `dispatchEvent`; all three current clients' synchronous history handlers cited above.
- **INFERENCE** — Let whatsmeow finish its internal history-derived session metadata storage, then apply walite's own age/count/byte retention before writing bodies. Evidence: tulir/whatsmeow, `message.go`, `DownloadHistorySync`.
- **INFERENCE** — Use one transaction per bounded batch, composite uniqueness for deduplication, indexed `(chat_id, timestamp, id)` pagination, and a pruning worker constrained by count/age/total bytes. Evidence basis: deficiencies in vicentereig/whatsapp-cli, `internal/store/store.go`, `StoreMessage`, and nchat, `MessageCache`.

## Ideas explicitly rejected

- **CONFIRMED rejection** — Rhymen/go-whatsapp, manual web versions, protocol reimplementation, browser embedding and automatic command-triggered replies are outside walite's rules and/or obsolete. Evidence: dimaskiddo/go-whatsapp-cli, `go.mod`, `WASyncVersion`, `WAHandler.HandleTextMessage`.
- **CONFIRMED rejection** — Unbounded in-memory archives, unbounded deques/goroutine spillover, synchronous SQLite work in event handlers, five-second polling, automatic media prefetch, and redraw-per-history-message conflict with bounded responsiveness. Evidence: normen/whatscli, `MessageDatabase`, `loadBacklog`; vicentereig/whatsapp-cli, `App.Sync`, `mediaDownloadWorker.Enqueue`; d99kris/nchat, `WmChat::SendRequest`, `MessageCache::EnqueueRequest`.
- **INFERENCE rejection** — Vendoring and patching whatsmeow is unnecessary for the proposed first milestones and increases update/licence burden; depend on maintained upstream unless a demonstrated blocker requires a narrowly documented patch. Evidence: d99kris/nchat, `lib/wmchat/go/go.mod`, `replace`; `utils/whatsmeow-update`.

## Unresolved technical questions

- **UNKNOWN** — Whether WhatsApp honors any pairing-time initial message/day/size fields as hard limits for this companion type. Evidence: tulir/whatsmeow, `DeviceProps.HistorySyncConfig`.
- **UNKNOWN** — Typical and worst-case decoded HistorySync blob sizes and concurrent event arrival patterns on real accounts; source cannot provide workload distributions. Evidence: tulir/whatsmeow, `DownloadHistorySync`, `dispatchEvent`.
- **UNKNOWN** — ON_DEMAND availability, exact count semantics, retry timing and terminal “no access” behavior across direct/group/LID chats. Evidence: tulir/whatsmeow, `BuildHistorySyncRequest`; `Conversation.EndOfHistoryTransferType`.
- **UNKNOWN** — Which history metadata walite must retain beyond whatsmeow's own stores for edits, revocations, replies, polls and future on-demand anchors. Evidence: tulir/whatsmeow, `waHistorySync.Conversation`, `ParseWebMessage`.
- **UNKNOWN** — A robust cross-platform way to enforce the 250 MB cap including SQLite WAL/journal and requested media while preserving newest-per-chat guarantees; this needs an offline store design experiment.

## Proposed minimal walite architecture

This is a proposal, therefore each item is an **INFERENCE** grounded in the reviewed boundaries rather than a confirmed property of existing code.

1. `internal/wa`: one adapter owns `*whatsmeow.Client`. Its callback classifies events and performs only nonblocking/short bounded enqueue. A small live queue and a separate history-descriptor queue have explicit capacities; live is selected first. History payload ownership and overflow behavior must be tested.
2. `internal/syncpolicy`: pure functions decide retain/discard using defaults of newest 100 per chat, 90 days and 250 MB. Metadata needed for session correctness remains whatsmeow's responsibility; application bodies outside policy are discarded.
3. `internal/store`: one SQLite writer goroutine accepts bounded write batches, commits bounded transactions, upserts on `(chat_id, message_id)`, indexes `(chat_id, timestamp DESC, message_id)`, and prunes incrementally. Queries return only the visible page. Database/private paths are created 0700 and files checked/chmodded 0600.
4. `internal/service`: owns cancellation, queue lifecycle and priority scheduling. It exposes read-only snapshots/deltas to the UI and media-download commands, never whatsmeow or SQL handles.
5. `internal/tui`: starts immediately from an empty/offline store, draws only changed regions, holds only visible chats/messages plus a small margin, and requests older cached/remote pages explicitly.
6. `internal/config`: XDG paths and configurable retention/queue/batch/redraw settings; media prefetch defaults off.
7. `cmd/walite`: signal-derived root context, construction and orderly shutdown: stop intake, disconnect, drain/abort bounded batches by policy, join workers, close store, restore terminal.

Evidence basis: tulir/whatsmeow, `dispatchEvent`, `DownloadHistorySync`; vicentereig/whatsapp-cli, injected interfaces and SQLite upserts; d99kris/nchat, `Protocol`, cache worker and paged UI; the confirmed weaknesses above.

## Proposed first offline implementation milestone

All work remains account-free and network-free:

1. **INFERENCE** — Define domain event types and interfaces for fake WA source, store, sync policy, clock and UI sink in the required package boundaries.
2. **INFERENCE** — Implement the retention decision engine and a deterministic priority coordinator with tiny bounded queues; tests prove live events overtake queued history, cancellation stops every goroutine, and overflow follows the documented policy.
3. **INFERENCE** — Implement an in-memory store fake only (no SQLite yet) with ID deduplication, newest-100/90-day behavior and bounded query pages.
4. **INFERENCE** — Add a headless fake UI sink proving it becomes ready before a large synthetic history feed completes and receives deltas rather than full redraws.
5. **INFERENCE** — Add race tests and allocation/benchmark fixtures using synthetic messages with fake names/numbers; do not import or connect whatsmeow until these contracts pass.

Evidence basis: normen/whatscli, `MessageDatabase.AddMessage`; vicentereig/whatsapp-cli, `NewAppWithDeps`; d99kris/nchat, `Protocol` and `MessageCache::Process`; tulir/whatsmeow, synchronous `dispatchEvent`.
