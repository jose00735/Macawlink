package com.macawlink.capture

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Rect
import android.hardware.camera2.CameraCaptureSession
import android.hardware.camera2.CameraCharacteristics
import android.hardware.camera2.CameraDevice
import android.hardware.camera2.CameraManager
import android.hardware.camera2.CaptureRequest
import android.hardware.camera2.params.OutputConfiguration
import android.hardware.camera2.params.SessionConfiguration
import android.media.MediaCodec
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.util.Log
import android.util.Range
import android.util.Size
import android.view.OrientationEventListener
import com.macawlink.codec.VideoEncoder
import com.macawlink.protocol.CameraInfo
import com.macawlink.protocol.ProtoJson
import com.macawlink.protocol.VideoConfigMsg
import com.macawlink.settings.StreamSettings
import java.util.concurrent.Executor
import kotlin.math.abs

/**
 * Camera2 → hardware encoder, zero copy. Owns its own thread. Live changes to torch, zoom
 * and stabilisation update the repeating request; size/fps/codec/lens restart the pipeline.
 */
class CameraController(
    private val context: Context,
    private val onConfig: (payloadJson: String, csd: ByteArray) -> Unit,
    private val onFrame: (data: ByteArray, ptsUs: Long, keyframe: Boolean) -> Unit,
    private val onError: (String) -> Unit,
    private val onStarted: (width: Int, height: Int, fps: Int, codec: String) -> Unit,
) {
    private val manager = context.getSystemService(CameraManager::class.java)
    private val thread = HandlerThread("mk-camera").apply { start() }
    private val handler = Handler(thread.looper)
    private val executor = Executor { handler.post(it) }

    private var device: CameraDevice? = null
    private var session: CameraCaptureSession? = null
    private var encoder: VideoEncoder? = null
    private var current: StreamSettings.Video? = null
    private var cameraId: String? = null
    private var sensorOrientation = 90
    private var facingFront = false
    private var actual = VideoConfigMsg("h264", 0, 0, 0, 0)
    private var lastCsd = ByteArray(0)
    @Volatile private var rotation = 0
    private var generation = 0
    private var preview: android.view.Surface? = null
    private var previewInSession = false

    private val orientation = object : OrientationEventListener(context) {
        override fun onOrientationChanged(deg: Int) {
            if (deg == ORIENTATION_UNKNOWN) return
            var d = (deg + 45) / 90 * 90 % 360
            if (facingFront) d = -d
            val r = (sensorOrientation + d + 360) % 360
            if (r != rotation) {
                rotation = r
                handler.post { if (lastCsd.isNotEmpty()) sendConfig() }
            }
        }
    }

    /** Capabilities for HELLO. */
    fun describeCameras(): List<CameraInfo> = manager.cameraIdList.mapNotNull { id ->
        runCatching {
            val ch = manager.getCameraCharacteristics(id)
            val facing = when (ch.get(CameraCharacteristics.LENS_FACING)) {
                CameraCharacteristics.LENS_FACING_FRONT -> "front"
                CameraCharacteristics.LENS_FACING_BACK -> "back"
                else -> "external"
            }
            val map = ch.get(CameraCharacteristics.SCALER_STREAM_CONFIGURATION_MAP)!!
            val sizes = map.getOutputSizes(MediaCodec::class.java).orEmpty()
                .filter { it.width <= 1920 && it.height <= 1080 }.map { listOf(it.width, it.height) }
            val fps = ch.get(CameraCharacteristics.CONTROL_AE_AVAILABLE_TARGET_FPS_RANGES).orEmpty().map { it.upper }.distinct().sorted()
            CameraInfo(id, facing, sizes, fps)
        }.getOrNull()
    }

    fun start(s: StreamSettings.Video) = handler.post { open(s) }

    /** Applies new settings; returns immediately. */
    fun update(s: StreamSettings.Video) = handler.post {
        val prev = current
        if (prev == null || needsRestart(prev, s)) {
            close()
            open(s)
            return@post
        }
        current = s
        if (prev.bitrateKbps != s.bitrateKbps) {
            encoder?.setBitrate(if (s.bitrateKbps > 0) s.bitrateKbps else VideoEncoder.autoBitrateKbps(actual.width, actual.height, actual.fps))
        }
        repeat(s)
    }

    fun requestKeyframe() = handler.post { encoder?.requestKeyframe() }

    /** Adaptive bitrate: set the encoder rate without restarting (kbps). */
    fun setBitrate(kbps: Int) = handler.post { encoder?.setBitrate(kbps) }

    /** Shows (or hides, with null) a live preview on the phone; rebuilds the capture session. */
    fun setPreview(surface: android.view.Surface?) = handler.post {
        if (surface == preview) return@post
        preview = surface
        val s = current ?: return@post
        if (device != null) { close(); open(s) }
    }

    fun encodedSize(): Pair<Int, Int> = actual.width to actual.height

    fun stop() = handler.post { close() }

    fun release() {
        handler.post { close() }
        thread.quitSafely()
    }

    private fun needsRestart(a: StreamSettings.Video, b: StreamSettings.Video) =
        a.width != b.width || a.height != b.height || a.fps != b.fps || a.codec != b.codec ||
            a.lens != b.lens || a.rateControl != b.rateControl

    @SuppressLint("MissingPermission") // CAMERA is granted before the service starts streaming
    private fun open(s: StreamSettings.Video) {
        val gen = ++generation
        current = s
        val id = pickCamera(s.lens) ?: return onError("No ${s.lens} camera on this phone")
        cameraId = id
        val ch = manager.getCameraCharacteristics(id)
        sensorOrientation = ch.get(CameraCharacteristics.SENSOR_ORIENTATION) ?: 90
        facingFront = ch.get(CameraCharacteristics.LENS_FACING) == CameraCharacteristics.LENS_FACING_FRONT
        val map = ch.get(CameraCharacteristics.SCALER_STREAM_CONFIGURATION_MAP)!!
        val (sw, sh) = pickSize(map.getOutputSizes(MediaCodec::class.java).orEmpty().map { it.width to it.height }, s.width, s.height)
        val size = Size(sw, sh)
        val range = pickFps(ch.get(CameraCharacteristics.CONTROL_AE_AVAILABLE_TARGET_FPS_RANGES).orEmpty().toList(), s.fps)

        var codecName = s.codec
        val enc = VideoEncoder(handler,
            onConfig = { csd -> lastCsd = csd; sendConfig() },
            onFrame = onFrame,
            onError = onError)
        var ok = enc.start(codecName, size.width, size.height, range.upper, s.bitrateKbps, s.rateControl == "vbr")
        if (!ok && codecName == "hevc") {
            codecName = "h264" // device has no HEVC encoder: fall back
            ok = enc.start(codecName, size.width, size.height, range.upper, s.bitrateKbps, s.rateControl == "vbr")
        }
        if (!ok) return onError("Encoder can't do ${size.width}×${size.height}")
        encoder = enc
        actual = VideoConfigMsg(codecName, size.width, size.height, range.upper, rotation)
        if (orientation.canDetectOrientation()) orientation.enable()

        manager.openCamera(id, executor, object : CameraDevice.StateCallback() {
            override fun onOpened(camera: CameraDevice) {
                if (gen != generation) { camera.close(); return }
                device = camera
                val surface = enc.inputSurface ?: return
                fun configure(withPreview: Boolean) {
                    val outputs = mutableListOf(OutputConfiguration(surface))
                    val pv = preview?.takeIf { withPreview && it.isValid }
                    if (pv != null) outputs += OutputConfiguration(pv)
                    val cfg = SessionConfiguration(SessionConfiguration.SESSION_REGULAR, outputs, executor,
                        object : CameraCaptureSession.StateCallback() {
                            override fun onConfigured(s2: CameraCaptureSession) {
                                if (gen != generation) { s2.close(); return }
                                session = s2
                                previewInSession = pv != null
                                repeat(s, range)
                                onStarted(size.width, size.height, range.upper, codecName)
                            }
                            override fun onConfigureFailed(s2: CameraCaptureSession) {
                                // Some phones can't feed encoder + preview at this size: stream without preview.
                                if (pv != null && gen == generation) configure(false) else onError("Camera session failed")
                            }
                        })
                    camera.createCaptureSession(cfg)
                }
                configure(true)
            }
            override fun onDisconnected(camera: CameraDevice) {
                camera.close()
                if (device == camera) device = null
                onError("Camera taken by another app")
            }
            override fun onError(camera: CameraDevice, error: Int) {
                camera.close()
                if (device == camera) device = null
                onError("Camera error $error")
            }
        })
    }

    private var lastRange: Range<Int> = Range(30, 30)

    private fun repeat(s: StreamSettings.Video, range: Range<Int> = lastRange) {
        val dev = device ?: return
        val sess = session ?: return
        val surface = encoder?.inputSurface ?: return
        lastRange = range
        val ch = manager.getCameraCharacteristics(cameraId!!)
        val req = dev.createCaptureRequest(CameraDevice.TEMPLATE_RECORD).apply {
            addTarget(surface)
            if (previewInSession) preview?.takeIf { it.isValid }?.let { addTarget(it) }
            set(CaptureRequest.CONTROL_AE_TARGET_FPS_RANGE, range)
            set(CaptureRequest.CONTROL_MODE, CaptureRequest.CONTROL_MODE_AUTO)
            set(CaptureRequest.CONTROL_AF_MODE, CaptureRequest.CONTROL_AF_MODE_CONTINUOUS_VIDEO)
            val stabModes = ch.get(CameraCharacteristics.CONTROL_AVAILABLE_VIDEO_STABILIZATION_MODES)
            if (stabModes?.contains(CaptureRequest.CONTROL_VIDEO_STABILIZATION_MODE_ON) == true) {
                set(CaptureRequest.CONTROL_VIDEO_STABILIZATION_MODE,
                    if (s.stabilization) CaptureRequest.CONTROL_VIDEO_STABILIZATION_MODE_ON
                    else CaptureRequest.CONTROL_VIDEO_STABILIZATION_MODE_OFF)
            }
            if (ch.get(CameraCharacteristics.FLASH_INFO_AVAILABLE) == true) {
                set(CaptureRequest.FLASH_MODE, if (s.torch) CaptureRequest.FLASH_MODE_TORCH else CaptureRequest.FLASH_MODE_OFF)
            }
            applyZoom(this, ch, s.zoom)
        }.build()
        runCatching { sess.setRepeatingRequest(req, null, handler) }.onFailure { Log.w(TAG, "repeat", it) }
    }

    private fun applyZoom(b: CaptureRequest.Builder, ch: CameraCharacteristics, zoom: Double) {
        if (Build.VERSION.SDK_INT >= 30) {
            val r = ch.get(CameraCharacteristics.CONTROL_ZOOM_RATIO_RANGE)
            if (r != null) {
                b.set(CaptureRequest.CONTROL_ZOOM_RATIO, zoom.toFloat().coerceIn(r.lower, r.upper))
                return
            }
        }
        val active = ch.get(CameraCharacteristics.SENSOR_INFO_ACTIVE_ARRAY_SIZE) ?: return
        val max = ch.get(CameraCharacteristics.SCALER_AVAILABLE_MAX_DIGITAL_ZOOM) ?: 1f
        val z = zoom.toFloat().coerceIn(1f, max)
        val w = (active.width() / z).toInt()
        val h = (active.height() / z).toInt()
        val x = (active.width() - w) / 2
        val y = (active.height() - h) / 2
        b.set(CaptureRequest.SCALER_CROP_REGION, Rect(x, y, x + w, y + h))
    }

    private fun sendConfig() {
        actual = actual.copy(rotation = rotation)
        onConfig(ProtoJson.encodeToString(VideoConfigMsg.serializer(), actual), lastCsd)
    }

    private fun close() {
        generation++
        orientation.disable()
        runCatching { session?.close() }
        runCatching { device?.close() }
        session = null
        device = null
        encoder?.stop()
        encoder = null
        lastCsd = ByteArray(0)
    }

    private fun pickCamera(lens: String): String? {
        val want = if (lens == "front") CameraCharacteristics.LENS_FACING_FRONT else CameraCharacteristics.LENS_FACING_BACK
        return manager.cameraIdList.firstOrNull {
            manager.getCameraCharacteristics(it).get(CameraCharacteristics.LENS_FACING) == want
        } ?: manager.cameraIdList.firstOrNull()
    }

    companion object {
        private const val TAG = "MacawlinkCamera"

        /** Exact size if offered, else the closest one with the same aspect, else the closest area. */
        fun pickSize(sizes: List<Pair<Int, Int>>, w: Int, h: Int): Pair<Int, Int> {
            sizes.firstOrNull { it.first == w && it.second == h }?.let { return it }
            val aspect = w.toDouble() / h
            val sameAspect = sizes.filter { abs(it.first.toDouble() / it.second - aspect) < 0.02 && it.first <= 1920 }
            val pool = sameAspect.ifEmpty { sizes.filter { it.first <= 1920 } }.ifEmpty { sizes }
            return pool.minByOrNull { abs(it.first * it.second - w * h) } ?: (w to h)
        }

        /** Prefer a fixed [fps, fps] range for steady timing, else the widest one topping at fps. */
        fun pickFps(ranges: List<Range<Int>>, fps: Int): Range<Int> {
            ranges.firstOrNull { it.lower == fps && it.upper == fps }?.let { return it }
            ranges.filter { it.upper == fps }.maxByOrNull { it.lower }?.let { return it }
            return ranges.minByOrNull { abs(it.upper - fps) * 100 - it.lower } ?: Range(30, 30)
        }
    }
}
