import Foundation

enum SessionRestoration {
    static func scriptSource(for uuid: String) -> String? {
        guard let uuid = NotificationRequest.normalizedSessionUUID(uuid) else { return nil }
        // Only the validated hexadecimal UUID enters this script. The traversal
        // is unchanged from the manually validated helper; AppleEvents are bounded.
        return """
        with timeout of 5 seconds
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
        end timeout
        """
    }

    static func restore(_ uuid: String) -> Bool {
        guard let source = scriptSource(for: uuid), let script = NSAppleScript(source: source) else { return false }
        var errorInfo: NSDictionary?
        return script.executeAndReturnError(&errorInfo).stringValue == "Walite restored"
    }
}
