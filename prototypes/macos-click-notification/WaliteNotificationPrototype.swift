import AppKit
import UserNotifications
import os

private let logger = Logger(subsystem: "io.github.antoinebaudrimontbeep.walite.clicknotificationprototype", category: "notification")

final class PrototypeDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    private let center = UNUserNotificationCenter.current()

    func applicationDidFinishLaunching(_ notification: Notification) {
        logger.info("applicationDidFinishLaunching")
    }

    func application(_ sender: NSApplication, openFiles filenames: [String]) {
        logger.info("openFiles reached: \(filenames.count) file(s)")
        guard filenames.count == 1,
              let raw = try? String(contentsOfFile: filenames[0], encoding: .utf8),
              let firstLine = raw.split(whereSeparator: \.isNewline).first,
              let uuid = UUID(uuidString: String(firstLine.split(separator: ":").last ?? ""))
        else {
            logger.error("invalid request file")
            sender.reply(toOpenOrPrint: .failure)
            return
        }
        sender.reply(toOpenOrPrint: .success)
        center.requestAuthorization(options: [.alert]) { granted, error in
            if let error { logger.error("authorization error: \(error.localizedDescription)"); return }
            logger.info("authorization granted: \(granted)")
            guard granted else { return }
            let content = UNMutableNotificationContent()
            content.title = "Walite click prototype"
            content.body = "Click this notification to restore this exact iTerm2 session."
            content.userInfo = ["sessionID": uuid.uuidString]
            let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
            self.center.add(request) { error in
                if let error { logger.error("add request error: \(error.localizedDescription)") }
                else { logger.info("notification request added") }
            }
        }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler completionHandler: @escaping () -> Void) {
        logger.info("notification response received")
        guard response.actionIdentifier == UNNotificationDefaultActionIdentifier,
              let rawID = response.notification.request.content.userInfo["sessionID"] as? String,
              let uuid = UUID(uuidString: rawID) else {
            logger.error("notification response has no valid session")
            completionHandler()
            return
        }

        let scriptSource = """
        tell application id "com.googlecode.iterm2"
            repeat with w in windows
                repeat with t in tabs of w
                    repeat with s in sessions of t
                        if (unique id of s) is "\(uuid.uuidString)" then
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
        completionHandler()
    }
}

let app = NSApplication.shared
let delegate = PrototypeDelegate()
UNUserNotificationCenter.current().delegate = delegate
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
