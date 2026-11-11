import Foundation

/// Names for files handed to the share sheet (chat downloads, mail attachments).
enum ChatDownloads {
    /// A suggested file name reduced to a single path component.
    static func fileName(_ suggested: String) -> String {
        let name = suggested
            .components(separatedBy: CharacterSet(charactersIn: "/\\:"))
            .joined(separator: "_")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty || name == "." || name == ".." ? "download" : name
    }
}
