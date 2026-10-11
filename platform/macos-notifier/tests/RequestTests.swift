import Foundation

@main
struct RequestTests {
    static func main() throws {
        let firstID = "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"
        let secondID = "11111111-2222-3333-4444-555555555555"
        let fixture: [String: Any] = [
            "version": 1, "notification_id": "1234567890", "session_uuid": firstID,
            "title": "JSON test \"quoted\" <>&", "body": "Unicode café 🐧\nsecond line"
        ]
        var checks = 0
        func encode(_ object: [String: Any]) throws -> Data {
            var data = try JSONSerialization.data(withJSONObject: object)
            data.append(10) // Go's json.Encoder.Encode adds a newline.
            return data
        }
        func expectRejected(_ data: Data) throws {
            do {
                _ = try NotificationRequest.parse(data)
            } catch {
                checks += 1
                return
            }
            throw NSError(domain: "RequestTests", code: 1)
        }
        let first = try NotificationRequest.parse(encode(fixture))
        guard first.notificationID == "1234567890", first.sessionUUID == firstID,
              first.title == fixture["title"] as? String, first.body == fixture["body"] as? String else {
            throw NSError(domain: "RequestTests", code: 2)
        }
        checks += 1
        try expectRejected(Data("{invalid JSON".utf8))
        try expectRejected(Data("[]".utf8))
        try expectRejected(Data("null".utf8))
        for key in ["version", "notification_id", "session_uuid", "title", "body"] {
            var missing = fixture
            missing.removeValue(forKey: key)
            try expectRejected(encode(missing))
            var wrongType = fixture
            wrongType[key] = key == "version" ? "1" : 1
            try expectRejected(encode(wrongType))
        }
        for version in [0, 2] {
            var unsupported = fixture
            unsupported["version"] = version
            try expectRejected(encode(unsupported))
        }
        for invalid in ["", "w0t0p0:" + firstID, firstID + " ", "not-a-uuid",
                        "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEZ"] {
            var request = fixture
            request["session_uuid"] = invalid
            try expectRejected(encode(request))
        }
        for emptyID in ["", " \n"] {
            var request = fixture
            request["notification_id"] = emptyID
            try expectRejected(encode(request))
        }
        var secondFixture = fixture
        secondFixture["notification_id"] = "0987654321"
        secondFixture["session_uuid"] = secondID.lowercased()
        secondFixture["title"] = ""
        secondFixture["body"] = ""
        let second = try NotificationRequest.parse(encode(secondFixture))
        guard second.sessionUUID == secondID, first.sessionUUID == firstID,
              second.notificationID != first.notificationID, second.title.isEmpty, second.body.isEmpty else {
            throw NSError(domain: "RequestTests", code: 3)
        }
        checks += 1
        print("request parser checks passed: \(checks)")
    }
}
