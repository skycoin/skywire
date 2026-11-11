import Darwin

/// The utun the system made for this extension. NetworkExtension does not hand
/// its descriptor out, so it is found as the open socket that answers
/// UTUN_OPT_IFNAME, as WireGuard's iOS app does.
enum UtunDescriptor {
    static func find() -> Int32? {
        var name = [CChar](repeating: 0, count: 16) // IFNAMSIZ
        for fd: Int32 in 0...1024 {
            var length = socklen_t(name.count)
            // SYSPROTO_CONTROL, UTUN_OPT_IFNAME
            if getsockopt(fd, 2, 2, &name, &length) == 0,
               String(decoding: name.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) }, as: UTF8.self).hasPrefix("utun") {
                return fd
            }
        }
        return nil
    }
}
