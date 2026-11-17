package com.macawlink.settings

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
        val codec: String = "h264",                         // h264 | hevc
        @SerialName("bitrate_kbps") val bitrateKbps: Int = 0, // 0 = auto
        @SerialName("rate_control") val rateControl: String = "cbr",
        val lens: String = "back",                          // back | wide | front
        val mirror: Boolean = false,
        val stabilization: Boolean = true,
    )

    @Serializable
    data class Audio(
        val enabled: Boolean = true,
        val codec: String = "opus",                         // opus | pcm
        @SerialName("sample_rate") val sampleRate: Int = 48_000,
        val channels: Int = 1,
        @SerialName("bitrate_kbps") val bitrateKbps: Int = 96,
        @SerialName("frame_ms") val frameMs: Int = 10,
        val aec: Boolean = true,
        val ns: Boolean = true,
        val agc: Boolean = true,
        @SerialName("gain_db") val gainDb: Int = 0,         // applied on the PC
    )

    companion object {
        val RESOLUTIONS = listOf(640 to 480, 1280 to 720, 1920 to 1080, 2560 to 1440, 3840 to 2160)
        val FRAME_RATES = listOf(15, 24, 30, 60)
        val SAMPLE_RATES = listOf(16_000, 24_000, 32_000, 44_100, 48_000)
    }
}
