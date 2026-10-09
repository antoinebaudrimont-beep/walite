import Foundation

struct NotificationRequest: Decodable {
    let version: Int
    let notificationID: String
    let sessionUUID: String
    let title: String
    let body: String

    enum CodingKeys: String, CodingKey {
        case version, title, body
        case notificationID = "notification_id"
        case sessionUUID = "session_uuid"
    }

    enum ValidationError: Error {
        case unsupportedVersion, emptyNotificationID, invalidSessionUUID
    }

    static func parse(_ data: Data) throws -> NotificationRequest {
        let request = try JSONDecoder().decode(NotificationRequest.self, from: data)
        guard request.version == 1 else { throw ValidationError.unsupportedVersion }
        guard !request.notificationID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw ValidationError.emptyNotificationID
        }
        guard let uuid = normalizedSessionUUID(request.sessionUUID) else {
            throw ValidationError.invalidSessionUUID
        }
        return NotificationRequest(version: request.version, notificationID: request.notificationID,
                                   sessionUUID: uuid, title: request.title, body: request.body)
    }

    // Match the Go backend's 36-byte hexadecimal UUID shape, without a pane prefix.
    static func normalizedSessionUUID(_ value: String) -> String? {
        let bytes = Array(value.utf8)
        guard bytes.count == 36 else { return nil }
        for (index, byte) in bytes.enumerated() {
            if [8, 13, 18, 23].contains(index) {
                guard byte == 45 else { return nil }
            } else {
                guard (48...57).contains(byte) || (65...70).contains(byte) || (97...102).contains(byte) else {
                    return nil
                }
            }
        }
        return value.uppercased()
    }
}
