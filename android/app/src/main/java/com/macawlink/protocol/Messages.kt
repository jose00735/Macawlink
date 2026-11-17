package com.macawlink.protocol

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/** JSON payloads from docs/PROTOCOL.md — mirror windows/internal/protocol/messages.go. */
val ProtoJson = Json { ignoreUnknownKeys = true; encodeDefaults = true }

@Serializable
data class HelloMsg(
    val model: String,
    val manufacturer: String,
    @SerialName("app_version") val appVersion: String,
    val protocol: Int = 1,
    val cameras: List<CameraInfo>,
    @SerialName("phone_id") val phoneId: String? = null, // Wi-Fi only
    val token: String? = null,                            // Wi-Fi: from a previous pairing
)

@Serializable
data class CameraInfo(val id: String, val facing: String, val sizes: List<List<Int>>, val fps: List<Int>)

@Serializable
data class HelloAckMsg(@SerialName("pc_name") val pcName: String = "", val protocol: Int = 1)

@Serializable
data class VideoConfigMsg(val codec: String, val width: Int, val height: Int, val fps: Int, val rotation: Int)

@Serializable
data class AudioConfigMsg(
    val codec: String,
    @SerialName("sample_rate") val sampleRate: Int,
    val channels: Int,
    @SerialName("frame_ms") val frameMs: Int,
)

@Serializable
data class StatsMsg(
    val battery: Int,
    val charging: Boolean,
    @SerialName("temp_c") val tempC: Double,
    val fps: Double,
    @SerialName("bitrate_kbps") val bitrateKbps: Int,
    val thermal: Int,
)

/** *_CONFIG payload: JSON, a 0x00 byte, then codec bytes. */
fun configPayload(json: String, codecData: ByteArray = ByteArray(0)): ByteArray {
    val js = json.toByteArray()
    return ByteArray(js.size + 1 + codecData.size).also {
        System.arraycopy(js, 0, it, 0, js.size)
        System.arraycopy(codecData, 0, it, js.size + 1, codecData.size)
    }
}

@Serializable
data class ByeMsg(val reason: String = "")

// ---- Wi-Fi pairing: numeric comparison with commitment (see windows/internal/pairing) ----

@Serializable
data class PairBeginMsg(val commit: String, @SerialName("pc_name") val pcName: String = "")

@Serializable
data class PairNonceMsg(val nonce: String)

@Serializable
data class PairConfirmMsg(val ok: Boolean)

@Serializable
data class PairResultMsg(val ok: Boolean, val token: String? = null, val reason: String? = null)

object Pairing {
    /** 6-digit code both screens show; must match windows/internal/protocol.SAS. */
    fun sas(certFp: ByteArray, pcNonce: ByteArray, phoneNonce: ByteArray): String {
        val md = java.security.MessageDigest.getInstance("SHA-256")
        md.update("macawlink-pair-v1".toByteArray())
        md.update(certFp)
        md.update(pcNonce)
        md.update(phoneNonce)
        val d = md.digest()
        val v = ((d[0].toLong() and 0xff) shl 24) or ((d[1].toLong() and 0xff) shl 16) or
            ((d[2].toLong() and 0xff) shl 8) or (d[3].toLong() and 0xff)
        return "%06d".format(v % 1_000_000)
    }

    fun commit(nonce: ByteArray): String = hex(java.security.MessageDigest.getInstance("SHA-256").digest(nonce))

    fun hex(b: ByteArray) = b.joinToString("") { "%02x".format(it) }
    fun unhex(s: String) = ByteArray(s.length / 2) { s.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
}
