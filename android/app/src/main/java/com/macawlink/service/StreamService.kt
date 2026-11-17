package com.macawlink.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.pm.ServiceInfo
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.lifecycle.LifecycleService
import androidx.lifecycle.lifecycleScope
import com.macawlink.R
import com.macawlink.capture.AudioPipeline
import com.macawlink.capture.CameraPipeline
import com.macawlink.codec.VideoEncoder
import com.macawlink.protocol.MsgType
import com.macawlink.protocol.Packet
import com.macawlink.protocol.Proto
import com.macawlink.protocol.mediaPayload
import com.macawlink.settings.StreamSettings
import com.macawlink.transport.Transport
import com.macawlink.transport.UsbTransport
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.util.concurrent.atomic.AtomicInteger

/**
 * Owns one streaming session. Foreground service of type camera|microphone so capture keeps
 * running with the screen off (required on Android 14+).
 */
class StreamService : LifecycleService() {
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }
    private val seq = AtomicInteger()
    private var settings = StreamSettings()
    private lateinit var transport: Transport
    private lateinit var camera: CameraPipeline
    private lateinit var encoder: VideoEncoder
    private lateinit var audio: AudioPipeline

    override fun onCreate() {
        super.onCreate()
        ServiceCompat.startForeground(
            this, 1, notification(),
            ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA or ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE,
        )
        transport = UsbTransport()
        camera = CameraPipeline(this, this)
        encoder = VideoEncoder(
            onConfig = { csd, v -> send(MsgType.VIDEO_CONFIG, Proto.FLAG_CONFIG, videoConfig(v, csd)) },
            onFrame = { data, pts, key ->
                send(MsgType.VIDEO_FRAME, if (key) Proto.FLAG_KEYFRAME else 0, mediaPayload(pts, data))
            },
        )
        // TODO(phase1): route through AudioEncoder (Opus) when settings.audio.codec == "opus".
        audio = AudioPipeline { pcm, pts -> send(MsgType.AUDIO_FRAME, 0, mediaPayload(pts, pcm)) }

        lifecycleScope.launch(Dispatchers.IO) {
            transport.connect()
            send(MsgType.HELLO, 0, json.encodeToString(mapOf("model" to android.os.Build.MODEL)).toByteArray())
            startPipelines()
            transport.incoming().collect { handle(it) }
        }
    }

    private fun startPipelines() {
        encoder.start(settings.video)
        lifecycleScope.launch(Dispatchers.Main) { camera.bind(settings.video, encoder.inputSurface) }
        if (settings.audio.enabled) audio.start(lifecycleScope, settings.audio)
        send(MsgType.CONFIG_STATE, 0, json.encodeToString(settings).toByteArray())
    }

    private fun handle(p: Packet) {
        when (p.type) {
            MsgType.SET_CONFIG -> applySettings(json.decodeFromString(p.payload.decodeToString()))
            MsgType.REQUEST_KEYFRAME -> encoder.requestKeyframe()
            MsgType.PING -> send(MsgType.PONG, 0, p.payload) // TODO: append receive timestamp
            MsgType.BYE -> stopSelf()
            else -> Unit
        }
    }

    /** Live tweak of resolution / fps / bitrate / audio format, from phone UI or PC tray. */
    private fun applySettings(next: StreamSettings) {
        val prev = settings
        settings = next
        if (encoder.needsRestart(next.video)) {
            lifecycleScope.launch(Dispatchers.Main) {
                camera.unbind(); encoder.stop()
                encoder.start(next.video)
                camera.bind(next.video, encoder.inputSurface)
            }
        } else if (prev.video.bitrateKbps != next.video.bitrateKbps && next.video.bitrateKbps > 0) {
            encoder.setBitrate(next.video.bitrateKbps)
        }
        if (prev.audio != next.audio) {
            audio.stop()
            if (next.audio.enabled) audio.start(lifecycleScope, next.audio)
            // TODO: send AUDIO_CONFIG for the new format
        }
        send(MsgType.CONFIG_STATE, 0, json.encodeToString(next).toByteArray())
    }

    private fun videoConfig(v: StreamSettings.Video, csd: ByteArray): ByteArray {
        // JSON header, NUL separator, then codec config bytes.
        val head = """{"codec":"${v.codec}","width":${v.width},"height":${v.height},"fps":${v.fps},"rotation":0}"""
        return head.toByteArray() + byteArrayOf(0) + csd
    }

    private fun send(type: MsgType, flags: Int, payload: ByteArray) {
        runCatching { transport.send(Packet(type, flags, seq.incrementAndGet(), payload)) }
    }

    private fun notification(): Notification {
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel("stream", getString(R.string.notif_channel), NotificationManager.IMPORTANCE_LOW))
        return NotificationCompat.Builder(this, "stream")
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(getString(R.string.notif_streaming))
            .setOngoing(true)
            .build()
    }

    override fun onDestroy() {
        send(MsgType.BYE, 0, """{"reason":"stopped"}""".toByteArray())
        audio.stop(); camera.unbind(); encoder.stop(); transport.close()
        super.onDestroy()
    }
}
