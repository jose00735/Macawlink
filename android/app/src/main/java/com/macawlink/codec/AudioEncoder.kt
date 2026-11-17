package com.macawlink.codec

import com.macawlink.settings.StreamSettings

/**
 * Opus via MediaCodec (MIMETYPE_AUDIO_OPUS, API 29+), fed with PCM from AudioPipeline.
 * When codec == "pcm" this is a passthrough and frames go to the transport as-is.
 *
 * TODO(phase1): MediaCodec "audio/opus" encoder, KEY_BIT_RATE = bitrateKbps*1000,
 * one 10/20 ms PCM frame in → one Opus packet out, emit CSD as AUDIO_CONFIG.
 */
interface AudioEncoder {
    fun start(s: StreamSettings.Audio, onConfig: (ByteArray) -> Unit, onPacket: (ByteArray, Long) -> Unit)
    fun encode(pcm: ByteArray, ptsUs: Long)
    fun stop()
}
