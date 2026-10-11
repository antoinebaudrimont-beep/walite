import Foundation
import ImageIO

@main
struct IconTests {
    static var checks = 0

    static func check(_ condition: Bool, _ label: String) throws {
        guard condition else { throw NSError(domain: "IconTests", code: 1, userInfo: [NSLocalizedDescriptionKey: label]) }
        checks += 1
    }

    static func run(_ executable: String, _ arguments: [String], environment: [String: String]? = nil) throws -> (Int32, Data) {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments
        if let environment { process.environment = environment }
        let output = Pipe()
        process.standardOutput = output
        process.standardError = output
        try process.run()
        let data = output.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        return (process.terminationStatus, data)
    }

    static func dimensions(_ url: URL) -> (Int, Int)? {
        guard let source = CGImageSourceCreateWithURL(url as CFURL, nil),
              let image = CGImageSourceCreateImageAtIndex(source, 0, nil) else { return nil }
        return (image.width, image.height)
    }

    static func main() {
        do { try test() }
        catch {
            FileHandle.standardError.write(Data("Icon checks failed: \(error.localizedDescription)\n".utf8))
            exit(1)
        }
    }

    static func test() throws {
        let directory = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
        let source = directory.appendingPathComponent("../../assets/walite-icon.png").standardizedFileURL
        let bytes = try Data(contentsOf: source)
        try check(bytes.prefix(8) == Data([137, 80, 78, 71, 13, 10, 26, 10]), "PNG signature")
        let dimensions = dimensions(source)
        try check(dimensions != nil, "source image decodes")
        try check(dimensions!.0 == dimensions!.1 && dimensions!.0 >= 1024, "square high-resolution source")

        let temporary = FileManager.default.temporaryDirectory.resolvingSymlinksInPath().appendingPathComponent("walite-icon-tests-" + UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: temporary, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
        defer { try? FileManager.default.removeItem(at: temporary) }
        let icon = temporary.appendingPathComponent("first.icns")
        let generator = directory.appendingPathComponent("generate-icon.sh").path
        try check(try run(generator, [source.path, icon.path]).0 == 0, "native conversion succeeds")
        let iconBytes = try Data(contentsOf: icon)
        try check(iconBytes.prefix(4) == Data("icns".utf8), "ICNS header")
        try check(iconBytes[4..<8].reduce(UInt32(0), { ($0 << 8) | UInt32($1) }) == iconBytes.count, "ICNS complete length")

        let decoded = temporary.appendingPathComponent("decoded.iconset")
        try check(try run("/usr/bin/iconutil", ["-c", "iconset", icon.path, "-o", decoded.path]).0 == 0, "ICNS roundtrip")
        for size in [16, 32, 128, 256, 512] {
            for retina in [false, true] {
                let suffix = retina ? "@2x" : ""
                let pixels = retina ? size * 2 : size
                let file = decoded.appendingPathComponent("icon_\(size)x\(size)\(suffix).png")
                let actual = Self.dimensions(file)
                try check(actual?.0 == pixels && actual?.1 == pixels, "representation \(file.lastPathComponent)")
            }
        }
        let second = temporary.appendingPathComponent("second.icns")
        try check(try run(generator, [source.path, second.path]).0 == 0, "repeat conversion")
        try check(try Data(contentsOf: second) == iconBytes, "reproducible ICNS with current macOS tools")
        try check(try Data(contentsOf: source) == bytes, "original PNG unchanged")
        try check(try run(generator, [source.path, icon.path]).0 != 0, "existing icon refused")
        try check(try Data(contentsOf: icon) == iconBytes, "refused icon unchanged")
        let invalid = temporary.appendingPathComponent("invalid.png")
        try Data("invalid PNG".utf8).write(to: invalid)
        try check(try run(generator, [invalid.path, temporary.appendingPathComponent("invalid.icns").path]).0 != 0, "malformed source rejected")
        try check(try run(generator, [temporary.appendingPathComponent("missing.png").path, temporary.appendingPathComponent("missing.icns").path]).0 != 0, "missing source rejected")
        let small = temporary.appendingPathComponent("small.png")
        try check(try run("/usr/bin/sips", ["-z", "32", "32", source.path, "--out", small.path]).0 == 0, "small test fixture")
        try check(try run(generator, [small.path, temporary.appendingPathComponent("small.icns").path]).0 != 0, "undersized source rejected without upscaling")
        let rectangular = temporary.appendingPathComponent("rectangular.png")
        try check(try run("/usr/bin/sips", ["-z", "1024", "1100", source.path, "--out", rectangular.path]).0 == 0, "rectangular test fixture")
        try check(try run(generator, [rectangular.path, temporary.appendingPathComponent("rectangular.icns").path]).0 != 0, "non-square source rejected without cropping")

        let build = temporary.appendingPathComponent("build", isDirectory: true)
        var environment = ProcessInfo.processInfo.environment
        environment["WALITE_NOTIFIER_BUILD_DIR"] = build.path
        environment["WALITE_TERMINAL_APP"] = "/invalid/obsolete-terminal-override.app"
        let result = try run(directory.appendingPathComponent("build.sh").path, [], environment: environment)
        guard result.0 == 0 else {
            FileHandle.standardError.write(result.1)
            try check(false, "native helper build")
            return
        }
        try check(true, "native helper build independent of terminal override")
        let app = build.appendingPathComponent("Walite Notifications.app", isDirectory: true).resolvingSymlinksInPath()
        let plistBytes = try Data(contentsOf: app.appendingPathComponent("Contents/Info.plist"))
        let plist = try PropertyListSerialization.propertyList(from: plistBytes, format: nil) as! [String: Any]
        try check(plist["CFBundleIdentifier"] as? String == "io.github.antoinebaudrimontbeep.walite.notifier", "permanent bundle identity")
        try check(plist["CFBundleName"] as? String == "Walite Notifications", "existing app name")
        try check(plist["CFBundleIconFile"] as? String == "Walite.icns", "icon metadata")
        try check(plist["LSUIElement"] as? Bool == true, "accessory behavior")
        try check((plist["CFBundleVersion"] as? String).flatMap(Int.init).map { $0 > 0 } == true, "explicit positive build number")
        try check(plist["CFBundleShortVersionString"] as? String == "0.1", "display version unchanged")
        try check(try Data(contentsOf: app.appendingPathComponent("Contents/Resources/Walite.icns")) == iconBytes, "built icon equals validated conversion")
        try check(try run("/usr/bin/plutil", ["-lint", app.appendingPathComponent("Contents/Info.plist").path]).0 == 0, "plist lint")
        try check(try run("/usr/bin/codesign", ["--verify", "--strict", app.path]).0 == 0, "signed after resources")
        let files = FileManager.default.enumerator(at: app, includingPropertiesForKeys: [.isRegularFileKey])!.compactMap { $0 as? URL }.filter { (try? $0.resourceValues(forKeys: [.isRegularFileKey]))?.isRegularFile == true }.map { $0.resolvingSymlinksInPath().path.replacingOccurrences(of: app.path + "/", with: "", options: [.anchored]) }
        try check(Set(files) == Set(["Contents/Info.plist", "Contents/MacOS/WaliteNotifier", "Contents/Resources/Walite.icns", "Contents/_CodeSignature/CodeResources"]), "only expected app files: \(files.sorted())")
        try check(try run(directory.appendingPathComponent("build.sh").path, [], environment: environment).0 != 0, "existing application refused")
        try check(try Data(contentsOf: app.appendingPathComponent("Contents/Info.plist")) == plistBytes, "refused app unchanged")
        print("official PNG, icon conversion and bundle checks passed: \(checks)")
    }
}
