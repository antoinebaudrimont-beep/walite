import AppKit
import UserNotifications
import os

private let logger = Logger(subsystem: "io.github.antoinebaudrimontbeep.walite.notifier", category: "notification")

final class NotifierDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    private let center = UNUserNotificationCenter.current()
    private var pendingRequests: [(request: NotificationRequest, path: String)] = []
    private var requestingAuthorization = false
    private let activationTargets = ChatActivationTargets()
    private let activationSender = ChatActivationSender()
    private let clickLock = NSLock()
    private var pendingClicks = 0

    func application(_ sender: NSApplication, openFiles filenames: [String]) {
        var rejected = false
        for filename in filenames {
            do {
                let request = try NotificationRequest.parse(Data(contentsOf: URL(fileURLWithPath: filename)))
                if request.rejectedActivationMetadata { logger.error("activation metadata rejected") }
                post(request, requestPath: filename)
            } catch {
                // Decoder errors and paths can contain private data; never log them.
                logger.error("request rejected")
                rejected = true
            }
        }
        sender.reply(toOpenOrPrint: rejected || filenames.isEmpty ? .failure : .success)
    }

    private func post(_ request: NotificationRequest, requestPath: String) {
        pendingRequests.append((request, requestPath))
        guard !requestingAuthorization else { return }
        requestingAuthorization = true
        center.requestAuthorization(options: [.alert]) { granted, error in
            DispatchQueue.main.async {
                let pending = self.pendingRequests
                self.pendingRequests.removeAll()
                self.requestingAuthorization = false
                guard error == nil else {
                    logger.error("notification authorization failed")
                    return
                }
                guard granted else {
                    logger.error("notification authorization denied")
                    return
                }
                for item in pending {
                    self.schedule(item.request, requestPath: item.path)
                }
            }
        }
    }

    private func schedule(_ request: NotificationRequest, requestPath: String) {
        let content = UNMutableNotificationContent()
        content.title = request.title
        content.body = request.body
        content.userInfo = ["session_uuid": request.sessionUUID]
        let notification = UNNotificationRequest(identifier: request.notificationID, content: content, trigger: nil)
        let generation = activationTargets.register(request)
        center.add(notification) { error in
            guard error == nil else {
                self.activationTargets.discard(request.notificationID, generation: generation)
                logger.error("notification request failed")
                return
            }
            logger.info("notification request accepted")
            do {
                try FileManager.default.removeItem(atPath: requestPath)
                logger.info("request file removed")
            } catch {
                logger.error("request file cleanup failed")
            }
        }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        defer { completionHandler() }
        guard response.actionIdentifier == UNNotificationDefaultActionIdentifier,
              let rawID = response.notification.request.content.userInfo["session_uuid"] as? String,
              let uuid = NotificationRequest.normalizedSessionUUID(rawID) else {
            logger.error("notification response rejected")
            return
        }
        logger.info("notification click received")
        clickLock.lock()
        guard pendingClicks < 8 else {
            clickLock.unlock()
            logger.error("notification click queue full")
            return
        }
        pendingClicks += 1
        clickLock.unlock()
        let target = activationTargets.take(response.notification.request.identifier, sessionUUID: uuid)
        // Finish the notification callback immediately. Keep AppleScript on the
        // main queue, followed by nonblocking socket work on a private worker.
        DispatchQueue.main.async {
            defer {
                self.clickLock.lock()
                self.pendingClicks -= 1
                self.clickLock.unlock()
            }
            guard SessionRestoration.restore(uuid) else {
                logger.error("session restore failed")
                return
            }
            logger.info("session restored")
            if let target = target {
                self.activationSender.submit(target) { result in
                    if result == .sent {
                        logger.info("activation command sent")
                    } else {
                        logger.error("activation command not sent")
                    }
                }
            }
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        activationSender.cancelAll()
    }
}

@main
struct WaliteNotifier {
    static func main() {
        let app = NSApplication.shared
        let delegate = NotifierDelegate()
        UNUserNotificationCenter.current().delegate = delegate
        app.delegate = delegate
        app.setActivationPolicy(.accessory)
        withExtendedLifetime(delegate) { app.run() }
    }
}
