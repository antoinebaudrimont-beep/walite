import AppKit
import UserNotifications
import os

private let logger = Logger(subsystem: "io.github.antoinebaudrimontbeep.walite.notifications", category: "notification")

final class NotifierDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    private let center = UNUserNotificationCenter.current()
    private var pendingRequests: [(request: NotificationRequest, path: String)] = []
    private var requestingAuthorization = false

    func application(_ sender: NSApplication, openFiles filenames: [String]) {
        var rejected = false
        for filename in filenames {
            do {
                let request = try NotificationRequest.parse(Data(contentsOf: URL(fileURLWithPath: filename)))
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
        center.add(notification) { error in
            guard error == nil else {
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

        // Preserve the window/tab/session traversal validated in the isolated prototype.
        // Only the validated hexadecimal UUID is interpolated, never notification text.
        let scriptSource = """
        tell application id "com.googlecode.iterm2"
            repeat with w in windows
                repeat with t in tabs of w
                    repeat with s in sessions of t
                        if (unique id of s) is "\(uuid)" then
                            set miniaturized of w to false
                            select w
                            select t
                            select s
                            activate
                            return "Walite restored"
                        end if
                    end repeat
                end repeat
            end repeat
        end tell
        error "Walite session not found"
        """
        var errorInfo: NSDictionary?
        if let script = NSAppleScript(source: scriptSource),
           script.executeAndReturnError(&errorInfo).stringValue == "Walite restored" {
            logger.info("session restored")
        } else {
            logger.error("session restore failed")
        }
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
