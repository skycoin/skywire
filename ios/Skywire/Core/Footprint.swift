import Darwin

/// The process's memory as iOS charges it.
enum Footprint {
    /// `phys_footprint` from TASK_VM_INFO, in bytes: what jetsam compares
    /// with a process's limit (the packet-tunnel extension's is about 50 MB).
    /// Not RSS, which also counts clean code pages and memory the Go runtime
    /// has already given back. nil if the kernel call fails.
    static func current() -> UInt64? {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(
            MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<natural_t>.size
        )
        let result = withUnsafeMutablePointer(to: &info) { pointer in
            pointer.withMemoryRebound(to: integer_t.self, capacity: Int(count)) { raw in
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), raw, &count)
            }
        }
        return result == KERN_SUCCESS ? info.phys_footprint : nil
    }
}
