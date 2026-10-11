import Foundation
import Darwin

struct ChatActivationTarget: Equatable {
    let chatID: String
    let socketPath: String
    let token: String

    init?(chatID: String, socketPath: String, token: String) {
        // Match model.NewChatID and the receiver's additional nonblank check.
        guard chatID.unicodeScalars.contains(where: { !$0.properties.isWhitespace }),
              chatID.utf8.count <= 512,
              !chatID.unicodeScalars.contains(where: { $0.properties.generalCategory == .control }),
              socketPath.hasPrefix("/"), socketPath.utf8.count <= 103,
              !socketPath.utf8.contains(0), !socketPath.hasSuffix("/"),
              !socketPath.split(separator: "/").contains(where: { $0 == "." || $0 == ".." }),
              token.utf8.count == 64,
              token.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }) else {
            return nil
        }
        self.chatID = chatID
        self.socketPath = socketPath
        self.token = token
    }

    func commandData() throws -> Data {
        struct Command: Encodable {
            let version = 1
            let chat_id: String
            let token: String
        }
        return try JSONEncoder().encode(Command(chat_id: chatID, token: token))
    }
}

// Credentials never enter UserNotifications.userInfo or files written by this helper.
// Oldest unclicked targets are evicted at capacity; their session restoration survives.
final class ChatActivationTargets {
    private struct Entry {
        let generation: UUID
        let sessionUUID: String
        let target: ChatActivationTarget
    }
    private let lock = NSLock()
    private let capacity: Int
    private var entries: [String: Entry] = [:]
    private var order: [String] = []

    init(capacity: Int = 256) { self.capacity = max(1, capacity) }

    @discardableResult
    func register(_ request: NotificationRequest) -> UUID {
        lock.lock()
        defer { lock.unlock() }
        let generation = UUID()
        entries.removeValue(forKey: request.notificationID)
        order.removeAll { $0 == request.notificationID }
        if let target = request.activation {
            if order.count == capacity {
                entries.removeValue(forKey: order.removeFirst())
            }
            entries[request.notificationID] = Entry(generation: generation, sessionUUID: request.sessionUUID, target: target)
            order.append(request.notificationID)
        }
        return generation
    }

    func discard(_ identifier: String, generation: UUID) {
        lock.lock()
        defer { lock.unlock() }
        if entries[identifier]?.generation == generation {
            entries.removeValue(forKey: identifier)
            order.removeAll { $0 == identifier }
        }
    }

    func take(_ identifier: String, sessionUUID: String) -> ChatActivationTarget? {
        lock.lock()
        defer { lock.unlock() }
        guard let entry = entries[identifier], entry.sessionUUID == sessionUUID else { return nil }
        entries.removeValue(forKey: identifier)
        order.removeAll { $0 == identifier }
        return entry.target
    }
}

final class ActivationCancellation {
    private let lock = NSLock()
    private var cancelled = false
    func cancel() { lock.lock(); cancelled = true; lock.unlock() }
    var isCancelled: Bool {
        lock.lock()
        defer { lock.unlock() }
        return cancelled
    }
}

enum ActivationResult: Equatable {
    case sent, connectionFailed, unsafeEndpoint, timedOut, cancelled, busy, invalidCommand
}

enum ChatActivationIPC {
    // Includes queueing, connect and all writes. Never wait for a receiver reply.
    static func send(_ target: ChatActivationTarget, deadline: DispatchTime,
                     cancellation: ActivationCancellation) -> ActivationResult {
        func interruption() -> ActivationResult? {
            if cancellation.isCancelled { return .cancelled }
            if DispatchTime.now() >= deadline { return .timedOut }
            return nil
        }
        if let result = interruption() { return result }
        guard let data = try? target.commandData(), data.count <= 4096 else { return .invalidCommand }

        // Go creates a same-user 0700 directory and 0600 socket. Reject symlinks,
        // permissive endpoints and other owners before transmitting credentials.
        var directory = stat()
        var endpoint = stat()
        let parent = (target.socketPath as NSString).deletingLastPathComponent
        guard lstat(parent, &directory) == 0,
              directory.st_mode & mode_t(S_IFMT) == mode_t(S_IFDIR),
              directory.st_uid == geteuid(), directory.st_mode & 0o777 == 0o700,
              lstat(target.socketPath, &endpoint) == 0 else { return .unsafeEndpoint }
        guard endpoint.st_mode & mode_t(S_IFMT) == mode_t(S_IFSOCK),
              endpoint.st_uid == geteuid(), endpoint.st_mode & 0o777 == 0o600 else { return .unsafeEndpoint }

        let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { return .connectionFailed }
        defer { close(descriptor) }
        let flags = fcntl(descriptor, F_GETFL)
        var noSignal: Int32 = 1
        guard flags >= 0, fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) == 0,
              fcntl(descriptor, F_SETFD, FD_CLOEXEC) == 0,
              setsockopt(descriptor, SOL_SOCKET, SO_NOSIGPIPE, &noSignal,
                         socklen_t(MemoryLayout<Int32>.size)) == 0 else { return .connectionFailed }

        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let path = Array(target.socketPath.utf8) + [0]
        withUnsafeMutableBytes(of: &address.sun_path) { destination in
            destination.copyBytes(from: path)
        }
        let connected = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        if connected != 0 {
            guard errno == EINPROGRESS || errno == EAGAIN || errno == EINTR else { return .connectionFailed }
            if let result = waitWritable(descriptor, deadline: deadline, cancellation: cancellation) { return result }
            var error: Int32 = 0
            var length = socklen_t(MemoryLayout<Int32>.size)
            guard getsockopt(descriptor, SOL_SOCKET, SO_ERROR, &error, &length) == 0, error == 0 else {
                return .connectionFailed
            }
        }
        var uid: uid_t = 0
        var gid: gid_t = 0
        var current = stat()
        guard getpeereid(descriptor, &uid, &gid) == 0, uid == geteuid(),
              lstat(target.socketPath, &current) == 0,
              current.st_dev == endpoint.st_dev, current.st_ino == endpoint.st_ino,
              current.st_uid == endpoint.st_uid, current.st_mode == endpoint.st_mode else { return .unsafeEndpoint }

        var offset = 0
        while offset < data.count {
            if let result = interruption() { return result }
            let count = data.withUnsafeBytes { buffer in
                Darwin.send(descriptor, buffer.baseAddress!.advanced(by: offset), data.count - offset, 0)
            }
            if count > 0 { offset += count; continue }
            if count < 0 && errno == EINTR { continue }
            if count < 0 && (errno == EAGAIN || errno == EWOULDBLOCK) {
                if let result = waitWritable(descriptor, deadline: deadline, cancellation: cancellation) { return result }
                continue
            }
            return .connectionFailed
        }
        if let result = interruption() { return result }
        // EOF is mandatory: Go reads to EOF before authenticating the object.
        return shutdown(descriptor, SHUT_WR) == 0 ? .sent : .connectionFailed
    }

    // Short bounded polls let cancellation interrupt a stalled connect/write.
    static func waitWritable(_ descriptor: Int32, deadline: DispatchTime,
                             cancellation: ActivationCancellation) -> ActivationResult? {
        while true {
            if cancellation.isCancelled { return .cancelled }
            let now = DispatchTime.now().uptimeNanoseconds
            guard now < deadline.uptimeNanoseconds else { return .timedOut }
            let milliseconds = Int32(min(20, max(1, (deadline.uptimeNanoseconds - now) / 1_000_000)))
            var item = pollfd(fd: descriptor, events: Int16(POLLOUT), revents: 0)
            let result = poll(&item, 1, milliseconds)
            if result > 0 {
                if item.revents & Int16(POLLNVAL) != 0 { return .connectionFailed }
                return nil // connect's SO_ERROR or send reports HUP/ERR.
            }
            if result < 0 && errno != EINTR { return .connectionFailed }
        }
    }
}

final class ChatActivationSender {
    private let queue: DispatchQueue
    private let lock = NSLock()
    private var jobs: [UUID: ActivationCancellation] = [:]
    private var stopped = false

    init(queue: DispatchQueue = DispatchQueue(label: "walite.notification-activation", qos: .utility)) {
        self.queue = queue
    }

    @discardableResult
    func submit(_ target: ChatActivationTarget, completion: @escaping (ActivationResult) -> Void) -> ActivationCancellation? {
        lock.lock()
        guard !stopped, jobs.count < 8 else { lock.unlock(); completion(.busy); return nil }
        let identifier = UUID()
        let cancellation = ActivationCancellation()
        jobs[identifier] = cancellation
        let deadline = DispatchTime.now() + .seconds(1)
        lock.unlock()
        queue.async {
            let result = ChatActivationIPC.send(target, deadline: deadline, cancellation: cancellation)
            self.lock.lock()
            self.jobs.removeValue(forKey: identifier)
            self.lock.unlock()
            completion(result)
        }
        return cancellation
    }

    func cancelAll() {
        lock.lock()
        stopped = true
        for job in jobs.values { job.cancel() }
        lock.unlock()
    }
}
