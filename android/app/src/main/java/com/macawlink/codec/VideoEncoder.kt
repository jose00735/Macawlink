package com.macawlink.codec

import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaFormat
import android.os.Bundle
import android.view.Surface
import com.macawlink.settings.StreamSettings

/**
 * Hardware H.264/HEVC encoder fed by a camera Surface. Async mode, realtime priority.
 * On a settings change, call [reconfigure]: it emits a fresh codec config (SPS/PPS) so the PC
 * re-inits its decoder while the virtual camera stays up.
 */
class VideoEncoder(
    private val onConfig: (csd: ByteArray, s: StreamSettings.Video) -> Unit,
    private val onFrame: (data: ByteArray, ptsUs: Long, keyframe: Boolean) -> Unit,
) {
    private var codec: MediaCodec? = null
    lateinit var inputSurface: Surface
        private set
    private var current: StreamSettings.Video? = null

    fun start(s: StreamSettings.Video, autoBitrateKbps: Int = 6000) {
        val mime = if (s.codec == "hevc") MediaFormat.MIMETYPE_VIDEO_HEVC else MediaFormat.MIMETYPE_VIDEO_AVC
        val bitrate = (if (s.bitrateKbps == 0) autoBitrateKbps else s.bitrateKbps) * 1000
        val fmt = MediaFormat.createVideoFormat(mime, s.width, s.height).apply {
            setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface)
            setInteger(MediaFormat.KEY_BIT_RATE, bitrate)
            setInteger(MediaFormat.KEY_FRAME_RATE, s.fps)
            setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 2)
            setInteger(MediaFormat.KEY_PRIORITY, 0)   // realtime
            setInteger(MediaFormat.KEY_LATENCY, 0)
            setInteger(
                MediaFormat.KEY_BITRATE_MODE,
                if (s.rateControl == "vbr") MediaCodecInfo.EncoderCapabilities.BITRATE_MODE_VBR
                else MediaCodecInfo.EncoderCapabilities.BITRATE_MODE_CBR,
            )
        }
        val c = MediaCodec.createEncoderByType(mime)
        c.setCallback(object : MediaCodec.Callback() {
            override fun onInputBufferAvailable(codec: MediaCodec, index: Int) = Unit // Surface input
            override fun onOutputBufferAvailable(codec: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
                val buf = codec.getOutputBuffer(index) ?: return
                val bytes = ByteArray(info.size).also { buf.position(info.offset); buf.get(it) }
                if (info.flags and MediaCodec.BUFFER_FLAG_CODEC_CONFIG != 0) {
                    onConfig(bytes, s)
                } else {
                    onFrame(bytes, info.presentationTimeUs, info.flags and MediaCodec.BUFFER_FLAG_KEY_FRAME != 0)
                }
                codec.releaseOutputBuffer(index, false)
            }
            override fun onError(codec: MediaCodec, e: MediaCodec.CodecException) { /* TODO: restart + notify PC */ }
            override fun onOutputFormatChanged(codec: MediaCodec, format: MediaFormat) = Unit
        })
        c.configure(fmt, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE)
        inputSurface = c.createInputSurface()
        c.start()
        codec = c
        current = s
    }

    /** PC asked (app opened the camera, or loss): next frame is an IDR. */
    fun requestKeyframe() {
        codec?.setParameters(Bundle().apply { putInt(MediaCodec.PARAMETER_KEY_REQUEST_SYNC_FRAME, 0) })
    }

    /** Bitrate can change live; resolution/fps/codec need a restart (caller rebinds the camera Surface). */
    fun setBitrate(kbps: Int) {
        codec?.setParameters(Bundle().apply { putInt(MediaCodec.PARAMETER_KEY_VIDEO_BITRATE, kbps * 1000) })
    }

    fun needsRestart(next: StreamSettings.Video): Boolean = current?.let {
        it.width != next.width || it.height != next.height || it.fps != next.fps ||
            it.codec != next.codec || it.rateControl != next.rateControl
    } ?: true

    fun stop() {
        runCatching { codec?.stop() }
        runCatching { codec?.release() }
        codec = null
    }
}
