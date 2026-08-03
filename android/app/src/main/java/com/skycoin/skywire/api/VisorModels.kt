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
)

@Serializable
data class HealthInfo(
    @SerialName("services_health") val servicesHealth: String = "",
    @SerialName("uptime_tracker_health") val uptimeTrackerHealth: String = "",
    @SerialName("autoconnect_health") val autoconnectHealth: String = "",
    @SerialName("transportability_health") val transportabilityHealth: String = "",
)

@Serializable
data class DmsgServerInfo(
    @SerialName("pk") val pk: String = "",
    /** Nanoseconds (Go time.Duration). */
    @SerialName("latency") val latencyNs: Long = 0,
)

/** GET /api/dmsg element — per-session round trip. */
@Serializable
data class DmsgClientSummary(
    @SerialName("public_key") val publicKey: String = "",
    @SerialName("server_public_key") val serverPublicKey: String = "",
    /** Nanoseconds (Go time.Duration). */
    @SerialName("round_trip") val roundTripNs: Long = 0,
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
