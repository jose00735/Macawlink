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
