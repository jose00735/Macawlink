package com.macawlink.codec

import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaFormat
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.util.Log
import android.view.Surface

/**
 * Hardware H.264/HEVC encoder with Surface input (the camera renders straight into it).
 * Async mode on the camera thread; realtime priority, no B-frames.
 */
class VideoEncoder(
    private val handler: Handler,
    private val onConfig: (csd: ByteArray) -> Unit,
    private val onFrame: (data: ByteArray, ptsUs: Long, keyframe: Boolean) -> Unit,
    private val onError: (String) -> Unit,
) {
    private var codec: MediaCodec? = null
    var inputSurface: Surface? = null
        private set
    var mime: String = MediaFormat.MIMETYPE_VIDEO_AVC
        private set

    /** Returns false if this codec/size is not supported by the device. */
    fun start(codecName: String, width: Int, height: Int, fps: Int, bitrateKbps: Int, vbr: Boolean): Boolean {
        mime = if (codecName == "hevc") MediaFormat.MIMETYPE_VIDEO_HEVC else MediaFormat.MIMETYPE_VIDEO_AVC
        val bitrate = (if (bitrateKbps > 0) bitrateKbps else autoBitrateKbps(width, height, fps)) * 1000
        val fmt = MediaFormat.createVideoFormat(mime, width, height).apply {
            setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface)
            setInteger(MediaFormat.KEY_BIT_RATE, bitrate)
            setInteger(MediaFormat.KEY_FRAME_RATE, fps)
            setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 2)
            setInteger(MediaFormat.KEY_PRIORITY, 0) // realtime
            setInteger(MediaFormat.KEY_MAX_B_FRAMES, 0)
            if (Build.VERSION.SDK_INT >= 30) setInteger(MediaFormat.KEY_LATENCY, 1)
            setInteger(
                MediaFormat.KEY_BITRATE_MODE,
                if (vbr) MediaCodecInfo.EncoderCapabilities.BITRATE_MODE_VBR
                else MediaCodecInfo.EncoderCapabilities.BITRATE_MODE_CBR,
            )
        }
        val c = try {
            MediaCodec.createEncoderByType(mime)
        } catch (e: Exception) {
            Log.w(TAG, "no encoder for $mime", e)
            return false
        }
        c.setCallback(object : MediaCodec.Callback() {
            override fun onInputBufferAvailable(codec: MediaCodec, index: Int) = Unit // Surface input

            override fun onOutputBufferAvailable(codec: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
                try {
                    val buf = codec.getOutputBuffer(index)
                    if (buf != null && info.size > 0) {
                        val bytes = ByteArray(info.size)
                        buf.position(info.offset)
                        buf.get(bytes)
                        if (info.flags and MediaCodec.BUFFER_FLAG_CODEC_CONFIG != 0) {
                            onConfig(bytes)
                        } else {
                            onFrame(bytes, info.presentationTimeUs, info.flags and MediaCodec.BUFFER_FLAG_KEY_FRAME != 0)
                        }
                    }
                    codec.releaseOutputBuffer(index, false)
                } catch (e: IllegalStateException) {
                    // codec stopped underneath us during a restart
                }
            }

            override fun onError(codec: MediaCodec, e: MediaCodec.CodecException) {
                Log.e(TAG, "encoder error", e)
                onError("Video encoder error: ${e.diagnosticInfo}")
            }

            override fun onOutputFormatChanged(codec: MediaCodec, format: MediaFormat) = Unit
        }, handler)
        return try {
            c.configure(fmt, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE)
            inputSurface = c.createInputSurface()
            c.start()
            codec = c
            Log.i(TAG, "encoder $mime ${width}x$height@$fps ${bitrate / 1000} kbps")
            true
        } catch (e: Exception) {
            Log.w(TAG, "encoder configure failed for $mime ${width}x$height", e)
            runCatching { c.release() }
            false
        }
    }

    fun requestKeyframe() {
        runCatching { codec?.setParameters(Bundle().apply { putInt(MediaCodec.PARAMETER_KEY_REQUEST_SYNC_FRAME, 0) }) }
    }

    fun setBitrate(kbps: Int) {
        runCatching { codec?.setParameters(Bundle().apply { putInt(MediaCodec.PARAMETER_KEY_VIDEO_BITRATE, kbps * 1000) }) }
    }

    fun stop() {
        runCatching { codec?.stop() }
        runCatching { codec?.release() }
        runCatching { inputSurface?.release() }
        codec = null
        inputSurface = null
    }

    companion object {
        private const val TAG = "MacawlinkEncoder"

        /** ~0.1 bit per pixel per frame: 720p30 ≈ 2.8 Mbps, 1080p30 ≈ 6.2 Mbps. */
        fun autoBitrateKbps(w: Int, h: Int, fps: Int): Int = (w.toLong() * h * fps / 10_000).toInt().coerceIn(800, 20_000)
    }
}
