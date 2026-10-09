import Foundation

struct NotificationRequest: Decodable {
    let version: Int
    let notificationID: String
    let sessionUUID: String
    let title: String
    let body: String
    let activation: ChatActivationTarget?
    let rejectedActivationMetadata: Bool

    enum CodingKeys: String, CodingKey {
        case version, title, body
        case notificationID = "notification_id"
        case sessionUUID = "session_uuid"
        case chatID = "chat_id"
        case activationSocket = "activation_socket"
        case activationToken = "activation_token"
    }

    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        version = try values.decode(Int.self, forKey: .version)
        notificationID = try values.decode(String.self, forKey: .notificationID)
        sessionUUID = try values.decode(String.self, forKey: .sessionUUID)
        title = try values.decode(String.self, forKey: .title)
        body = try values.decode(String.self, forKey: .body)
        let supplied = [CodingKeys.chatID, .activationSocket, .activationToken].contains { values.contains($0) }
        if let chatID = try? values.decode(String.self, forKey: .chatID),
           let socket = try? values.decode(String.self, forKey: .activationSocket),
           let token = try? values.decode(String.self, forKey: .activationToken) {
            activation = ChatActivationTarget(chatID: chatID, socketPath: socket, token: token)
        } else {
            activation = nil
        }
        rejectedActivationMetadata = supplied && activation == nil
    }

    private init(version: Int, notificationID: String, sessionUUID: String, title: String,
                 body: String, activation: ChatActivationTarget?, rejectedActivationMetadata: Bool) {
        self.version = version
        self.notificationID = notificationID
        self.sessionUUID = sessionUUID
        self.title = title
        self.body = body
        self.activation = activation
        self.rejectedActivationMetadata = rejectedActivationMetadata
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
                                   sessionUUID: uuid, title: request.title, body: request.body,
                                   activation: request.activation,
                                   rejectedActivationMetadata: request.rejectedActivationMetadata)
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
