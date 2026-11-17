package com.macawlink.service

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.os.BatteryManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.macawlink.R
import com.macawlink.capture.AudioCapture
import com.macawlink.capture.CameraController
import com.macawlink.protocol.AudioConfigMsg
import com.macawlink.protocol.HelloAckMsg
import com.macawlink.protocol.HelloMsg
import com.macawlink.protocol.MsgType
import com.macawlink.protocol.Packet
import com.macawlink.protocol.Proto
import com.macawlink.protocol.ProtoJson
import com.macawlink.protocol.StatsMsg
import com.macawlink.protocol.configPayload
import com.macawlink.protocol.mediaPayload
import com.macawlink.session.SessionHub
import com.macawlink.session.SessionHub.Phase
import com.macawlink.settings.SettingsStore
import com.macawlink.settings.StreamSettings
import com.macawlink.transport.Connection
import com.macawlink.transport.PcLink
import com.macawlink.ui.MainActivity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.concurrent.thread

/**
 * Foreground service (camera|microphone) that waits for the PC over USB and streams while
 * it is connected. Camera and mic are only open while a PC is attached.
 */
class StreamService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var store: SettingsStore
    private val link = PcLink()
    @Volatile private var running = false
    @Volatile private var conn: Connection? = null
    private var camera: CameraController? = null
    private var audio: AudioCapture? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private var sessionJob: Job? = null

    // stats
    @Volatile private var dropUntilKey = false
    private var framesWindow = 0
    private var bytesWindow = 0L
    private var windowStart = System.nanoTime()

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        store = SettingsStore(this)
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }
        val n = notification(getString(R.string.notif_waiting))
        if (Build.VERSION.SDK_INT >= 30) {
            ServiceCompat.startForeground(this, NOTIF_ID, n, fgsTypes())
        } else {
            startForeground(NOTIF_ID, n) // camera/microphone FGS types only exist from Android 11
        }
        if (!running) {
            running = true
            wakeLock = getSystemService(PowerManager::class.java)
                .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "macawlink:stream").apply { acquire(12 * 60 * 60 * 1000L) }
            SessionHub.update { it.copy(phase = Phase.WAITING_PC, error = null) }
            thread(name = "mk-accept") { acceptLoop() }
        }
        return START_NOT_STICKY
    }

    @androidx.annotation.RequiresApi(30)
    private fun fgsTypes(): Int {
        var t = ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA
        if (hasPermission(Manifest.permission.RECORD_AUDIO)) t = t or ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE
        return t
    }

    private fun hasPermission(p: String) = ContextCompat.checkSelfPermission(this, p) == PackageManager.PERMISSION_GRANTED

    private fun acceptLoop() {
        try {
            link.open()
        } catch (e: Exception) {
            SessionHub.update { it.copy(error = "Port ${Proto.DEFAULT_PORT} busy: ${e.message}") }
            stopSelf()
            return
        }
        while (running) {
            val c = try { link.accept() } catch (e: Exception) { if (running) Log.w(TAG, "accept", e); break }
            runSession(c)
        }
    }

    private fun runSession(c: Connection) {
        conn = c
        dropUntilKey = false
        val settings = SessionHub.settings.value
        Log.i(TAG, "PC connected")

        val cam = CameraController(this,
            onConfig = { json, csd -> c.send(MsgType.VIDEO_CONFIG, configPayload(json, csd), Proto.FLAG_CONFIG) },
            onFrame = ::onVideoFrame,
            onError = { msg -> SessionHub.update { it.copy(error = msg) } },
            onStarted = { w, h, fps, _ -> SessionHub.update { it.copy(width = w, height = h, error = null) } })
        camera = cam

        c.send(MsgType.HELLO, ProtoJson.encodeToString(HelloMsg.serializer(), HelloMsg(
            model = Build.MODEL, manufacturer = Build.MANUFACTURER.replaceFirstChar { it.uppercase() },
            appVersion = APP_VERSION, cameras = cam.describeCameras())).toByteArray())
        sendState(c, settings)

        cam.start(settings.video)
        startAudio(c, settings.audio)
        SessionHub.update { it.copy(phase = Phase.STREAMING, audioOn = audio != null) }
        updateNotification(getString(R.string.notif_streaming))

        // Settings changes from either side flow through SessionHub.settings.
        sessionJob = scope.launch {
            var prev = settings
            launch {
                SessionHub.settings.collect { next ->
                    if (next == prev) return@collect
                    applySettings(c, prev, next)
                    prev = next
                }
            }
            while (isActive) {
                delay(2000)
                sendStats(c)
            }
        }

        try {
            while (!c.closed) {
                val p = c.read()
                handle(c, p)
                if (p.type == MsgType.BYE) break
            }
        } catch (e: Exception) {
            Log.i(TAG, "PC link closed: ${e.message}")
        } finally {
            sessionJob?.cancel()
            cam.release()
            audio?.stop()
            audio = null
            camera = null
            c.close()
            conn = null
            SessionHub.update { it.copy(phase = if (running) Phase.WAITING_PC else Phase.STOPPED, pcName = null, fps = 0.0, kbps = 0) }
            if (running) updateNotification(getString(R.string.notif_waiting))
        }
    }

    private fun handle(c: Connection, p: Packet) {
        when (p.type) {
            MsgType.HELLO_ACK -> {
                val ack = runCatching { ProtoJson.decodeFromString(HelloAckMsg.serializer(), p.payload.decodeToString()) }.getOrNull()
                SessionHub.update { it.copy(pcName = ack?.pcName?.ifBlank { null } ?: "PC") }
                updateNotification(getString(R.string.notif_streaming_to, ack?.pcName ?: "PC"))
            }
            MsgType.SET_CONFIG -> runCatching {
                val next = ProtoJson.decodeFromString(StreamSettings.serializer(), p.payload.decodeToString())
                store.save(next)
                SessionHub.settings.value = next // collector applies it and answers CONFIG_STATE
            }.onFailure { Log.w(TAG, "bad SET_CONFIG", it) }
            MsgType.REQUEST_KEYFRAME -> camera?.requestKeyframe()
            MsgType.PING -> {
                val pong = ByteBuffer.allocate(16).order(ByteOrder.BIG_ENDIAN)
                pong.put(p.payload, 0, minOf(8, p.payload.size)).position(8)
                pong.putLong(System.nanoTime())
                c.send(MsgType.PONG, pong.array())
            }
            else -> Unit
        }
    }

    private fun applySettings(c: Connection, prev: StreamSettings, next: StreamSettings) {
        if (prev.video != next.video) camera?.update(next.video)
        if (prev.audio != next.audio) startAudio(c, next.audio)
        sendState(c, next)
    }

    private fun sendState(c: Connection, s: StreamSettings) =
        c.send(MsgType.CONFIG_STATE, ProtoJson.encodeToString(StreamSettings.serializer(), s).toByteArray())

    private fun startAudio(c: Connection, a: StreamSettings.Audio) {
        audio?.stop()
        audio = null
        if (!a.enabled || !hasPermission(Manifest.permission.RECORD_AUDIO)) {
            SessionHub.update { it.copy(audioOn = false) }
            return
        }
        val cap = AudioCapture { pcm, pts -> c.send(MsgType.AUDIO_FRAME, mediaPayload(pts, pcm)) }
        c.send(MsgType.AUDIO_CONFIG, configPayload(ProtoJson.encodeToString(AudioConfigMsg.serializer(),
            AudioConfigMsg("pcm", a.sampleRate, a.channels, a.frameMs))))
        if (cap.start(a)) audio = cap
        SessionHub.update { it.copy(audioOn = audio != null) }
    }

    private fun onVideoFrame(data: ByteArray, ptsUs: Long, key: Boolean) {
        val c = conn ?: return
        if (dropUntilKey && !key) return
        if (c.sendVideo(mediaPayload(ptsUs, data), if (key) Proto.FLAG_KEYFRAME else 0)) {
            dropUntilKey = false
            synchronized(this) { framesWindow++; bytesWindow += data.size }
        } else if (!dropUntilKey) {
            dropUntilKey = true // USB link congested: skip to the next keyframe
            camera?.requestKeyframe()
        }
    }

    private fun sendStats(c: Connection) {
        val now = System.nanoTime()
        val (frames, bytes) = synchronized(this) { val r = framesWindow to bytesWindow; framesWindow = 0; bytesWindow = 0; r }
        val secs = (now - windowStart) / 1e9
        windowStart = now
        val fps = if (secs > 0) frames / secs else 0.0
        val kbps = if (secs > 0) (bytes * 8 / secs / 1000).toInt() else 0
        val battery = registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        val level = battery?.getIntExtra(BatteryManager.EXTRA_LEVEL, -1) ?: -1
        val scale = battery?.getIntExtra(BatteryManager.EXTRA_SCALE, 100) ?: 100
        val plugged = (battery?.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0) ?: 0) != 0
        val temp = (battery?.getIntExtra(BatteryManager.EXTRA_TEMPERATURE, 0) ?: 0) / 10.0
        val thermal = getSystemService(PowerManager::class.java).currentThermalStatus
        c.send(MsgType.STATS, ProtoJson.encodeToString(StatsMsg.serializer(), StatsMsg(
            battery = if (level >= 0) level * 100 / scale else 0, charging = plugged, tempC = temp,
            fps = fps, bitrateKbps = kbps, thermal = thermal)).toByteArray())
        SessionHub.update { it.copy(fps = fps, kbps = kbps) }
    }

    override fun onDestroy() {
        running = false
        conn?.let { it.send(MsgType.BYE, """{"reason":"stopped on phone"}""".toByteArray()); Thread.sleep(50); it.close() }
        link.close()
        camera?.release()
        audio?.stop()
        scope.cancel()
        wakeLock?.let { if (it.isHeld) it.release() }
        SessionHub.update { SessionHub.LinkState(phase = Phase.STOPPED) }
        super.onDestroy()
    }

    // ---- notification ----

    private fun createChannel() {
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(CHANNEL, getString(R.string.notif_channel), NotificationManager.IMPORTANCE_LOW))
    }

    private fun notification(text: String): Notification {
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val stop = PendingIntent.getService(this, 1, Intent(this, StreamService::class.java).setAction(ACTION_STOP), PendingIntent.FLAG_IMMUTABLE)
        return NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_macawlink)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(text)
            .setOngoing(true)
            .setContentIntent(open)
            .addAction(0, getString(R.string.action_stop), stop)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .build()
    }

    private fun updateNotification(text: String) {
        getSystemService(NotificationManager::class.java).notify(NOTIF_ID, notification(text))
    }

    companion object {
        private const val TAG = "MacawlinkService"
        private const val CHANNEL = "stream"
        private const val NOTIF_ID = 1
        const val ACTION_STOP = "com.macawlink.STOP"
        const val APP_VERSION = "0.1.0"

        fun start(ctx: Context) = ContextCompat.startForegroundService(ctx, Intent(ctx, StreamService::class.java))
        fun stop(ctx: Context) = ctx.startService(Intent(ctx, StreamService::class.java).setAction(ACTION_STOP))
    }
}
