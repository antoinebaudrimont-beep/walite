import Foundation
import Darwin

private struct TestFailure: Error { let check: String }

private final class LocalReceiver {
    let directory: String
    let path: String
    private(set) var descriptor: Int32

    init() throws {
        directory = "/private/tmp/walite-ipc-" + UUID().uuidString.prefix(8)
        path = directory + "/receiver.sock"
        try FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: false,
                                               attributes: [.posixPermissions: 0o700])
        descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw TestFailure(check: "listener creation") }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { $0.copyBytes(from: Array(path.utf8) + [0]) }
        let bound = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bound == 0, chmod(path, 0o600) == 0, listen(descriptor, 8) == 0 else {
            throw TestFailure(check: "listener setup")
        }
    }

    deinit {
        if descriptor >= 0 { close(descriptor) }
        try? FileManager.default.removeItem(atPath: directory)
    }

    func stop() {
        close(descriptor)
        descriptor = -1
    }

    func readThroughEOF() throws -> Data {
        var incoming = pollfd(fd: descriptor, events: Int16(POLLIN), revents: 0)
        guard poll(&incoming, 1, 1500) > 0 else { throw TestFailure(check: "accept deadline") }
        let client = accept(descriptor, nil, nil)
        guard client >= 0 else { throw TestFailure(check: "accept") }
        defer { close(client) }
        var data = Data()
        var bytes = [UInt8](repeating: 0, count: 4097)
        while true {
            var readable = pollfd(fd: client, events: Int16(POLLIN), revents: 0)
            guard poll(&readable, 1, 1500) > 0 else { throw TestFailure(check: "EOF framing deadline") }
            let count = recv(client, &bytes, bytes.count, 0)
            guard count >= 0 else { throw TestFailure(check: "read") }
            if count == 0 { return data }
            data.append(contentsOf: bytes.prefix(count))
            guard data.count <= 4096 else { throw TestFailure(check: "receiver size limit") }
        }
    }
}

@main
struct ActivationTests {
    static func main() throws {
        let token = String(repeating: "a", count: 64)
        let firstUUID = "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"
        let secondUUID = "11111111-2222-3333-4444-555555555555"
        let base: [String: Any] = ["version": 1, "notification_id": "A", "session_uuid": firstUUID,
                                   "title": "Synthetic test", "body": "No personal data"]
        let metadata: [String: Any] = ["chat_id": "chat-a", "activation_socket": "/tmp/private/receiver.sock",
                                      "activation_token": token]
        var checks = 0
        func check(_ condition: Bool, _ name: String) throws {
            guard condition else { throw TestFailure(check: name) }
            checks += 1
        }
        func parse(_ values: [String: Any]) throws -> NotificationRequest {
            try NotificationRequest.parse(JSONSerialization.data(withJSONObject: values))
        }
        func activated(_ additions: [String: Any] = [:]) throws -> NotificationRequest {
            try parse(base.merging(metadata) { _, new in new }.merging(additions) { _, new in new })
        }
        let ordinary = try parse(base)
        try check(ordinary.activation == nil && !ordinary.rejectedActivationMetadata, "version-1 ordinary request")
        let first = try activated()
        try check(first.activation?.chatID == "chat-a" && !first.rejectedActivationMetadata, "valid metadata")
        for key in metadata.keys {
            var incomplete = base.merging(metadata) { _, new in new }
            incomplete.removeValue(forKey: key)
            let request = try parse(incomplete)
            try check(request.activation == nil && request.rejectedActivationMetadata && request.title == ordinary.title,
                      "incomplete metadata preserves ordinary request")
            for invalid: Any in [NSNull(), 12, [], [:]] {
                let request = try activated([key: invalid])
                try check(request.activation == nil && request.rejectedActivationMetadata, "wrong optional type")
            }
        }
        for value in ["", " \t", "a\n", "a\u{007f}", "a\u{0085}", String(repeating: "x", count: 513),
                      String(repeating: "é", count: 257)] {
            try check(try activated(["chat_id": value]).activation == nil, "invalid ChatID")
        }
        for value in ["chat@lid", "opaque chat", "chat\"<&>\\🐧", String(repeating: "é", count: 256), "a\u{200d}b", "\u{200b}"] {
            try check(try activated(["chat_id": value]).activation?.chatID == value, "neutral Go identifier rules")
        }
        for value in ["", "relative.sock", "/tmp/../s.sock", "/tmp/./s.sock", "/tmp/s.sock\0", "/tmp/",
                      "/" + String(repeating: "x", count: 103), "/" + String(repeating: "é", count: 52)] {
            try check(try activated(["activation_socket": value]).activation == nil, "invalid socket path")
        }
        try check(try activated(["activation_socket": "/" + String(repeating: "x", count: 102)]).activation != nil,
                  "103-byte socket limit")
        for value in ["", String(repeating: "a", count: 63), String(repeating: "A", count: 64),
                      String(repeating: "g", count: 64), token + "\n"] {
            try check(try activated(["activation_token": value]).activation == nil, "invalid token")
        }

        let second = try activated(["notification_id": "B", "session_uuid": secondUUID,
                                    "chat_id": "chat-b", "activation_token": String(repeating: "b", count: 64),
                                    "activation_socket": "/tmp/another/receiver.sock"])
        let targets = ChatActivationTargets(capacity: 2)
        let firstGeneration = targets.register(first)
        targets.register(second)
        try check(targets.take("A", sessionUUID: secondUUID) == nil, "UUID binding")
        try check(targets.take("A", sessionUUID: firstUUID) == first.activation, "A after B keeps own target")
        try check(targets.take("B", sessionUUID: secondUUID) == second.activation, "B keeps own target")
        try check(targets.take("A", sessionUUID: firstUUID) == nil, "single-use target")
        targets.register(first)
        targets.discard("A", generation: firstGeneration)
        try check(targets.take("A", sessionUUID: firstUUID) != nil, "old scheduling failure cannot delete replacement")
        let failedGeneration = targets.register(first)
        targets.discard("A", generation: failedGeneration)
        try check(targets.take("A", sessionUUID: firstUUID) == nil, "failed notification removes credentials")
        targets.register(first)
        targets.register(ordinary)
        try check(targets.take("A", sessionUUID: firstUUID) == nil, "ordinary replacement clears credentials")
        targets.register(first)
        targets.register(second)
        targets.register(try activated(["notification_id": "C"]))
        try check(targets.take("A", sessionUUID: firstUUID) == nil, "bounded credential retention")

        let escaped = try activated(["chat_id": "chat\"<&>\\🐧"]).activation!
        let command = try JSONSerialization.jsonObject(with: escaped.commandData()) as! [String: Any]
        try check(Set(command.keys) == ["version", "chat_id", "token"] && command["version"] as? Int == 1 &&
                  command["chat_id"] as? String == escaped.chatID && command["token"] as? String == token,
                  "exact receiver command and escaped opaque ID")

        let receiver = try LocalReceiver()
        let liveTarget = ChatActivationTarget(chatID: "chat-a", socketPath: receiver.path, token: token)!
        func send(_ target: ChatActivationTarget) -> ActivationResult {
            ChatActivationIPC.send(target, deadline: .now() + .seconds(1), cancellation: ActivationCancellation())
        }
        try check(send(liveTarget) == .sent, "local socket send")
        let received = try JSONSerialization.jsonObject(with: receiver.readThroughEOF()) as! [String: Any]
        try check(received["chat_id"] as? String == "chat-a" && received["token"] as? String == token &&
                  received["version"] as? Int == 1 && received.count == 3, "real receiver command and EOF")
        let otherReceiver = try LocalReceiver()
        let liveSecond = ChatActivationTarget(chatID: "chat-b", socketPath: otherReceiver.path,
                                              token: String(repeating: "b", count: 64))!
        try check(send(liveSecond) == .sent && send(liveTarget) == .sent, "two independent endpoints B then A")
        let receivedB = try JSONSerialization.jsonObject(with: otherReceiver.readThroughEOF()) as! [String: Any]
        let receivedA = try JSONSerialization.jsonObject(with: receiver.readThroughEOF()) as! [String: Any]
        try check(receivedB["chat_id"] as? String == "chat-b" && receivedA["chat_id"] as? String == "chat-a",
                  "endpoint isolation")
        let missing = ChatActivationTarget(chatID: "chat-a", socketPath: receiver.directory + "/missing.sock", token: token)!
        try check(send(missing) != .sent, "unavailable socket")
        try check(chmod(receiver.path, 0o666) == 0 && send(liveTarget) == .unsafeEndpoint, "public socket rejected")
        try check(chmod(receiver.path, 0o600) == 0 && chmod(receiver.directory, 0o755) == 0 &&
                  send(liveTarget) == .unsafeEndpoint, "public parent rejected")
        try check(chmod(receiver.directory, 0o700) == 0, "private permissions restored")
        let link = receiver.directory + "/link.sock"
        try FileManager.default.createSymbolicLink(atPath: link, withDestinationPath: receiver.path)
        try check(send(ChatActivationTarget(chatID: "chat-a", socketPath: link, token: token)!) == .unsafeEndpoint,
                  "socket symlink rejected")
        let stopped = try LocalReceiver()
        stopped.stop()
        try check(send(ChatActivationTarget(chatID: "chat-a", socketPath: stopped.path, token: token)!) == .connectionFailed,
                  "closed listener connection failure")
        let cancelled = ActivationCancellation()
        cancelled.cancel()
        try check(ChatActivationIPC.send(liveTarget, deadline: .now() + .seconds(1), cancellation: cancelled) == .cancelled,
                  "pre-cancelled send")
        try check(ChatActivationIPC.send(liveTarget, deadline: .now(), cancellation: ActivationCancellation()) == .timedOut,
                  "expired send deadline")

        // Force genuine socket backpressure without sending authentication data.
        var pair: [Int32] = [-1, -1]
        guard socketpair(AF_UNIX, SOCK_STREAM, 0, &pair) == 0 else { throw TestFailure(check: "socketpair") }
        defer { close(pair[0]); close(pair[1]) }
        guard fcntl(pair[0], F_SETFL, O_NONBLOCK) == 0 else { throw TestFailure(check: "nonblocking socketpair") }
        let filler = [UInt8](repeating: 0, count: 65536)
        while filler.withUnsafeBytes({ Darwin.send(pair[0], $0.baseAddress!, $0.count, 0) }) > 0 {}
        try check(errno == EAGAIN || errno == EWOULDBLOCK, "real write backpressure")
        let start = DispatchTime.now().uptimeNanoseconds
        try check(ChatActivationIPC.waitWritable(pair[0], deadline: .now() + .milliseconds(80),
                                                cancellation: ActivationCancellation()) == .timedOut,
                  "stalled socket timeout")
        try check(DispatchTime.now().uptimeNanoseconds - start < 500_000_000, "timeout is bounded")
        let midFlight = ActivationCancellation()
        DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(40)) { midFlight.cancel() }
        let cancelStart = DispatchTime.now().uptimeNanoseconds
        try check(ChatActivationIPC.waitWritable(pair[0], deadline: .now() + .seconds(1), cancellation: midFlight) == .cancelled,
                  "mid-wait cancellation")
        try check(DispatchTime.now().uptimeNanoseconds - cancelStart < 500_000_000, "cancellation is bounded")

        let sender = ChatActivationSender()
        let completed = DispatchSemaphore(value: 0)
        sender.submit(liveTarget) { result in
            if result == .sent && !Thread.isMainThread { completed.signal() }
        }
        try check(completed.wait(timeout: .now() + .seconds(2)) == .success, "asynchronous worker completion")
        _ = try receiver.readThroughEOF()
        sender.cancelAll()
        var rejectedAfterStop = false
        let admission = sender.submit(liveTarget) { rejectedAfterStop = $0 == .busy }
        try check(admission == nil && rejectedAfterStop, "shutdown stops admission")

        let heldQueue = DispatchQueue(label: "walite.test-held-worker")
        heldQueue.suspend()
        let boundedSender = ChatActivationSender(queue: heldQueue)
        let cancelledJobs = DispatchSemaphore(value: 0)
        var admitted: [ActivationCancellation] = []
        for _ in 0..<8 {
            if let job = boundedSender.submit(liveTarget, completion: { result in
                if result == .cancelled { cancelledJobs.signal() }
            }) { admitted.append(job) }
        }
        var queueFull = false
        let overflow = boundedSender.submit(liveTarget) { queueFull = $0 == .busy }
        try check(admitted.count == 8 && overflow == nil && queueFull, "eight-job admission bound")
        boundedSender.cancelAll()
        heldQueue.resume()
        for _ in 0..<8 {
            try check(cancelledJobs.wait(timeout: .now() + .seconds(1)) == .success, "queued work cancelled on shutdown")
        }
        let delayedQueue = DispatchQueue(label: "walite.test-delayed-worker")
        delayedQueue.suspend()
        let delayedSender = ChatActivationSender(queue: delayedQueue)
        let expiredJob = DispatchSemaphore(value: 0)
        delayedSender.submit(liveTarget) { result in
            if result == .timedOut { expiredJob.signal() }
        }
        DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(1100)) { delayedQueue.resume() }
        try check(expiredJob.wait(timeout: .now() + .seconds(2)) == .success, "queue delay counts toward send deadline")

        let source = SessionRestoration.scriptSource(for: firstUUID)!
        try check(source.contains("with timeout of 5 seconds") && source.contains("set miniaturized of w to false") &&
                  source.contains("select w\n") && source.contains("select t\n") && source.contains("select s\n") &&
                  source.contains("repeat with s in sessions of t") && source.contains(firstUUID), "restoration traversal")
        var scriptError: NSDictionary?
        try check(NSAppleScript(source: source)?.compileAndReturnError(&scriptError) == true, "native AppleScript compile")
        try check(SessionRestoration.scriptSource(for: "\"\nactivate") == nil, "restoration rejects injection")
        try check(SessionRestoration.scriptSource(for: ordinary.sessionUUID) == source, "ordinary restoration unchanged")
        print("activation parser, targeting, IPC and restoration checks passed: \(checks)")
    }
}
