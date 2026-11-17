package com.macawlink.settings

import android.content.Context
import com.macawlink.protocol.ProtoJson
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Same JSON as StreamSettings in docs/PROTOCOL.md and windows/internal/config. */
@Serializable
data class StreamSettings(
    val video: Video = Video(),
    val audio: Audio = Audio(),
) {
    @Serializable
    data class Video(
        val width: Int = 1280,
        val height: Int = 720,
        val fps: Int = 30,
        val codec: String = "h264",                           // h264 | hevc
        @SerialName("bitrate_kbps") val bitrateKbps: Int = 0, // 0 = auto
        @SerialName("rate_control") val rateControl: String = "cbr",
        val lens: String = "back",                            // back | front
        val mirror: Boolean = false,                          // applied on the PC
        val stabilization: Boolean = true,
        val torch: Boolean = false,
        val zoom: Double = 1.0,
    )

    @Serializable
    data class Audio(
        val enabled: Boolean = true,
        val codec: String = "pcm",
        @SerialName("sample_rate") val sampleRate: Int = 48_000,
        val channels: Int = 1,
        @SerialName("frame_ms") val frameMs: Int = 10,
        val aec: Boolean = true,
        val ns: Boolean = true,
        val agc: Boolean = true,
        @SerialName("gain_db") val gainDb: Int = 0,           // applied on the PC
    )

    companion object {
        val RESOLUTIONS = listOf(640 to 480, 1280 to 720, 1920 to 1080)
        val FRAME_RATES = listOf(15, 24, 30, 60)
        val SAMPLE_RATES = listOf(16_000, 24_000, 32_000, 44_100, 48_000)
        val BITRATES = listOf(0, 1500, 3000, 6000, 10000, 15000, 20000)

        val PRESETS = linkedMapOf(
            "Low" to StreamSettings(
                Video(640, 480, 30, bitrateKbps = 1500),
                Audio(sampleRate = 16_000, frameMs = 20),
            ),
            "Meeting" to StreamSettings(),
            "Max" to StreamSettings(
                Video(1920, 1080, 60, bitrateKbps = 20000, rateControl = "vbr"),
                Audio(channels = 2, aec = false, ns = false, agc = false),
            ),
        )
    }
}

/** Persists settings across launches. */
class SettingsStore(context: Context) {
    private val prefs = context.getSharedPreferences("macawlink", Context.MODE_PRIVATE)

    fun load(): StreamSettings = prefs.getString("settings", null)
        ?.let { runCatching { ProtoJson.decodeFromString<StreamSettings>(it) }.getOrNull() }
        ?: StreamSettings()

    fun save(s: StreamSettings) {
        prefs.edit().putString("settings", ProtoJson.encodeToString(StreamSettings.serializer(), s)).apply()
    }
}
