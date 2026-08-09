package com.skycoin.skywire.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/**
 * DTOs for the visor's local API. Only the fields the app renders are
 * declared — the decoder ignores everything else, so server-side additions
 * never break the client.
 */

@Serializable
data class About(
    @SerialName("public_key") val publicKey: String,
    @SerialName("dmsg_connected") val dmsgConnected: Boolean = false,
    @SerialName("dmsg_sessions") val dmsgSessions: Int = 0,
)

@Serializable
data class VisorSummary(
    @SerialName("overview") val overview: Overview = Overview(),
    @SerialName("health") val health: HealthInfo? = null,
    /** Seconds since visor start. */
    @SerialName("uptime") val uptime: Double = 0.0,
    @SerialName("dmsg_servers") val dmsgServers: List<DmsgServerInfo> = emptyList(),
    @SerialName("build_tag") val buildTag: String = "",
    @SerialName("config_version") val configVersion: String = "",
    /**
     * Whether this visor answered the summary RPC. Only meaningful in the
     * `/api/visors-summary` list, where an offline visor is still listed —
     * served from the last snapshot, with every other field stale.
     */
    @SerialName("online") val online: Boolean = false,
    /** True for the visor serving the API — this phone, in the Fleet list. */
    @SerialName("is_hypervisor") val isHypervisor: Boolean = false,
    /** RFC 3339. Last successful summary; absent for a never-seen visor. */
    @SerialName("last_seen_at") val lastSeenAt: String? = null,
    /** RFC 3339, set only while [online] is false. */
    @SerialName("offline_since") val offlineSince: String? = null,
)

@Serializable
data class Overview(
    @SerialName("local_pk") val localPk: String = "",
    @SerialName("build_info") val buildInfo: BuildInfo? = null,
    @SerialName("apps") val apps: List<AppState> = emptyList(),
    @SerialName("transports") val transports: List<TransportSummary> = emptyList(),
    @SerialName("local_ip") val localIp: String = "",
    /**
     * The address the visor's STUN probe saw at startup — the phone's
     * UNDERLAY address, and not a field to present as "your IP" without
     * saying so: the app excludes its own UID from the SkyVPN tunnel (it
     * carries that tunnel), so this reads the same whether or not SkyVPN is
     * connected. It also carries two non-IP shapes — empty behind symmetric
     * NAT, and the NAT-type word itself when the probe failed — which is why
     * [publicIpOrNull] exists rather than reading this directly.
     */
    @SerialName("public_ip") val publicIp: String = "",
    /** Typo is the wire format's, not ours (`is_symmetic_nat` in api.go). */
    @SerialName("is_symmetic_nat") val isSymmetricNat: Boolean = false,
    @SerialName("nat_type") val natType: String = "",
    /** Where the visor appears to be, resolved by a dmsg server at startup. */
    @SerialName("country_code") val countryCode: String = "",
) {
    /**
     * [publicIp] when it is actually an address, else null.
     *
     * The visor writes the NAT type into this field when STUN fails
     * (`api_visor.go`: NATError/NATUnknown/NATBlocked all store
     * `NATType.String()`), and leaves it empty behind symmetric NAT. Both
     * would otherwise be rendered to the user as though they were addresses.
     */
    val publicIpOrNull: String?
        get() = publicIp.takeIf { candidate ->
            candidate.isNotEmpty() &&
                candidate.any { it == '.' || it == ':' } &&
                candidate.all { it.isDigit() || it == '.' || it == ':' || it in 'a'..'f' || it in 'A'..'F' }
        }
}

/**
 * One live transport. A phone's are almost always dmsg, but which carrier
 * actually came up is the first thing to look at when a route won't build —
 * hence the type, not just a count.
 */
@Serializable
data class TransportSummary(
    @SerialName("id") val id: String = "",
    @SerialName("remote_pk") val remotePk: String = "",
    @SerialName("type") val type: String = "",
    @SerialName("latency_ms") val latencyMs: Double = 0.0,
)

@Serializable
data class BuildInfo(
    @SerialName("version") val version: String = "",
    @SerialName("commit") val commit: String = "",
    @SerialName("date") val date: String = "",
)

@Serializable
data class AppState(
    @SerialName("name") val name: String,
    /** 0 stopped · 1 running · 2 errored · 3 starting. */
    @SerialName("status") val status: Int = 0,
    @SerialName("detailed_status") val detailedStatus: String = "",
    @SerialName("auto_start") val autoStart: Boolean = false,
    @SerialName("port") val port: Int = 0,
    /**
     * Launcher argv. The API always sends the array form — the
     * space-joined string in the config file is an on-disk-only
     * rendering that never reaches this client.
     */
    @SerialName("args") val args: List<String> = emptyList(),
) {
    val running: Boolean get() = status == STATUS_RUNNING

    companion object {
        const val STATUS_RUNNING = 1
        const val STATUS_ERRORED = 2
        const val STATUS_STARTING = 3
    }
}

/**
 * One service-discovery entry, as proxied verbatim from SD by
 * `/api/svc-fetch`. Only the fields the list renders are declared.
 */
@Serializable
data class ServiceEntry(
    /** `"<public key>:<port>"`. */
    @SerialName("address") val address: String = "",
    @SerialName("type") val type: String = "",
    @SerialName("geo") val geo: GeoInfo? = null,
    @SerialName("version") val version: String = "",
) {
    val pk: String get() = address.substringBefore(':')
}

@Serializable
data class GeoInfo(
    @SerialName("country") val country: String = "",
    @SerialName("region") val region: String = "",
)

/**
 * One live connection of an app, from `…/apps/{app}/connections`.
 * skysocks-client and vpn-client each hold a single connection to their
 * server, so the first element is the one the screens render.
 */
@Serializable
data class AppConnection(
    @SerialName("is_alive") val isAlive: Boolean = false,
    /**
     * Milliseconds, despite the field being a Go `time.Duration`: the visor
     * converts before serializing (`ConnectionsSummary` in
     * pkg/app/appserver/proc.go), so the number on the wire is already a
     * millisecond count and must not be divided again.
     */
    @SerialName("latency") val latencyMs: Long = 0,
    /** Bytes per second. */
    @SerialName("upload_speed") val uploadSpeed: Long = 0,
    @SerialName("download_speed") val downloadSpeed: Long = 0,
    @SerialName("bandwidth_sent") val bandwidthSent: Long = 0,
    @SerialName("bandwidth_received") val bandwidthReceived: Long = 0,
    /** Seconds the tunnel has been carrying traffic; 0 when it is not. */
    @SerialName("connection_duration") val connectionSeconds: Long = 0,
    @SerialName("error") val error: String = "",
)

/**
 * GET …/apps/{app}/stats. [startTime] is when the app's process started —
 * absent while it isn't running, and earlier than the tunnel's own uptime
 * ([AppConnection.connectionSeconds]), which only starts at "connected".
 */
@Serializable
data class AppStats(
    @SerialName("connections") val connections: List<AppConnection>? = null,
    /** RFC 3339, as Go renders a `*time.Time`. */
    @SerialName("start_time") val startTime: String? = null,
)

@Serializable
data class HealthInfo(
    @SerialName("services_health") val servicesHealth: String = "",
    @SerialName("uptime_tracker_health") val uptimeTrackerHealth: String = "",
    @SerialName("autoconnect_health") val autoconnectHealth: String = "",
    @SerialName("transportability_health") val transportabilityHealth: String = "",
)

/**
 * One dmsg server this visor holds a session with, from the summary's
 * `dmsg_servers`. The visor answers it from its own live session list, so
 * it is populated whenever dmsg is up — unlike `/api/dmsg`, whose entries
 * come from the round-trip tracker and need a dmsgctrl dial back to the
 * tracked visor (for the local one, a self-dial that the phone never wins:
 * "dmsg error 202 — cannot connect to delegated server").
 */
@Serializable
data class DmsgServerInfo(
    @SerialName("pk") val pk: String = "",
    /**
     * Nanoseconds (Go time.Duration). Measured by a self-ping through the
     * server, hourly — 0 until the first one lands, and it can stay 0 on a
     * phone, so it is only rendered when > 0.
     */
    @SerialName("latency") val latencyNs: Long = 0,
    /** Raw session carrier: tcp | ws | wt | quic. */
    @SerialName("carrier") val carrier: String = "",
    /** Human-readable form of [carrier] (e.g. "tcp", "wss", "quic"). */
    @SerialName("protocol") val protocol: String = "",
)

/**
 * GET/PUT …/router-settings — the visor-wide router knobs, as one struct.
 * The PUT applies *every* field, so a caller changing one must send the
 * others back unchanged (see [VisorApi.setTransportPreference]).
 */
@Serializable
data class RouterSettings(
    @SerialName("force_local_routes") val forceLocalRoutes: Boolean = false,
    @SerialName("existing_tp_only") val existingTpOnly: Boolean = false,
    @SerialName("mux_routes") val muxRoutes: Int = 0,
    @SerialName("min_hops") val minHops: Int = 0,
    /** Transport-type priority order, most-preferred first. */
    @SerialName("transport_preference") val transportPreference: List<String> = emptyList(),
)

@Serializable
data class ServiceHealthEntry(
    @SerialName("name") val name: String = "",
    @SerialName("status") val status: String = "",
    @SerialName("latency_ms") val latencyMs: Double = 0.0,
    @SerialName("transport") val transport: String = "",
    @SerialName("error") val error: String = "",
)

/** GET …/runtime-logs?since=N — `entries` is JSON null on an empty buffer. */
@Serializable
data class RuntimeLogsDelta(
    @SerialName("entries") val entries: List<String>? = null,
    @SerialName("latest") val latest: Long = 0,
    @SerialName("dropped") val dropped: Long = 0,
)

@Serializable
data class AppLogs(
    @SerialName("last_log_timestamp") val lastLogTimestamp: String = "",
    @SerialName("logs") val logs: List<String> = emptyList(),
)

/** One call being placed, from `…/skychat/voice/dialing`. */
@Serializable
internal data class VoiceDialing(
    @SerialName("call_id") val callId: String = "",
    @SerialName("peer") val peer: String = "",
)

/**
 * A ringing inbound call. The visor formats these as `"<call-id> from <pk>"`
 * — one string, because the surface it was built for is a CLI listing — so the
 * shape is parsed here rather than deserialized.
 */
data class VoiceInvite(val callId: String, val fromPk: String) {
    companion object {
        private const val SEPARATOR = " from "

        /** null when the line isn't the expected shape, so a poll can skip it. */
        fun parse(line: String): VoiceInvite? {
            val at = line.indexOf(SEPARATOR)
            if (at <= 0) return null
            val id = line.substring(0, at).trim()
            val pk = line.substring(at + SEPARATOR.length).trim()
            return if (id.isEmpty() || pk.isEmpty()) null else VoiceInvite(id, pk)
        }
    }
}

@Serializable
internal data class Credentials(
    @SerialName("username") val username: String,
    @SerialName("password") val password: String,
)

@Serializable
internal data class UserExists(@SerialName("exists") val exists: Boolean = false)

@Serializable
internal data class CsrfToken(@SerialName("csrf_token") val token: String = "")

@Serializable
internal data class ApiError(@SerialName("error") val error: String = "")
