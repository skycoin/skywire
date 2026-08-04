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
)

@Serializable
data class Overview(
    @SerialName("local_pk") val localPk: String = "",
    @SerialName("build_info") val buildInfo: BuildInfo? = null,
    @SerialName("apps") val apps: List<AppState> = emptyList(),
    @SerialName("transports") val transports: List<JsonElement> = emptyList(),
    @SerialName("local_ip") val localIp: String = "",
    @SerialName("public_ip") val publicIp: String = "",
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
 * skysocks-client holds a single connection to its server, so the
 * first element is the one the screen renders.
 */
@Serializable
data class AppConnection(
    @SerialName("is_alive") val isAlive: Boolean = false,
    /** Nanoseconds (Go time.Duration). */
    @SerialName("latency") val latencyNs: Long = 0,
    @SerialName("upload_speed") val uploadSpeed: Long = 0,
    @SerialName("download_speed") val downloadSpeed: Long = 0,
    @SerialName("bandwidth_sent") val bandwidthSent: Long = 0,
    @SerialName("bandwidth_received") val bandwidthReceived: Long = 0,
    @SerialName("error") val error: String = "",
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
