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
import com.macawlink.codec.VideoEncoder
import com.macawlink.protocol.AudioConfigMsg
import com.macawlink.protocol.ByeMsg
import com.macawlink.protocol.HelloAckMsg
import com.macawlink.protocol.HelloMsg
import com.macawlink.protocol.MsgType
import com.macawlink.protocol.Packet
import com.macawlink.protocol.PairBeginMsg
import com.macawlink.protocol.PairConfirmMsg
import com.macawlink.protocol.PairNonceMsg
import com.macawlink.protocol.PairResultMsg
import com.macawlink.protocol.Pairing
import com.macawlink.protocol.Proto
import com.macawlink.protocol.ProtoJson
import com.macawlink.protocol.StatsMsg
import com.macawlink.protocol.configPayload
import com.macawlink.protocol.mediaPayload
import com.macawlink.session.SessionHub
import com.macawlink.session.SessionHub.Phase
import com.macawlink.settings.SettingsStore
import com.macawlink.settings.StreamSettings
import com.macawlink.settings.WifiStore
import com.macawlink.transport.Connection
import com.macawlink.transport.PcLink
import com.macawlink.transport.WifiDialer
import com.macawlink.ui.MainActivity
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import java.io.IOException
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.security.SecureRandom
import kotlin.concurrent.thread

/**
 * Foreground service (camera|microphone). Waits for the PC over USB (loopback listener) and,
 * when a Wi-Fi PC is chosen, keeps dialing it. One PC session at a time; camera and mic are
 * open only while a PC is attached.
 */
class StreamService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var store: SettingsStore
    private lateinit var wifiStore: WifiStore
    private val link = PcLink()
    @Volatile private var running = false
    @Volatile private var conn: Connection? = null
    private val sessionLock = Any()
    @Volatile private var busy = false
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
        wifiStore = WifiStore(this)
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
            thread(name = "mk-wifi") { wifiLoop() }
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

    // ---- connections ----

    private fun acceptLoop() {
        try {
            link.open()
        } catch (e: Exception) {
            SessionHub.update { it.copy(error = "USB port ${Proto.DEFAULT_PORT} busy: ${e.message}") }
            return
        }
        while (running) {
            val c = try { link.accept() } catch (e: Exception) { if (running) Log.w(TAG, "accept", e); break }
            thread(name = "mk-usb-session") { guarded(c) { runSession(c, "USB", null) } }
        }
    }

    /** Keeps a session to the chosen Wi-Fi PC alive (reconnects with backoff). */
    private fun wifiLoop() {
        var backoff = 1000L
        while (running) {
            val target = SessionHub.wifiTarget.value
            if (target == null || busy) { Thread.sleep(500); continue }
            val paired = wifiStore.pairedFor(target.shortFp)
            SessionHub.update { it.copy(phase = Phase.CONNECTING, error = null) }
            val dialed = try {
                WifiDialer.dial(target.host, target.port, paired?.fp?.let { Pairing.unhex(it) })
            } catch (e: Exception) {
                if (!running) break
                SessionHub.update { it.copy(phase = Phase.WAITING_PC, error = "Can't reach ${target.name} (${target.host}): ${e.message}") }
                Thread.sleep(backoff)
                backoff = (backoff * 2).coerceAtMost(10_000)
                continue
            }
            backoff = 1000L
            guarded(dialed.connection) { runSession(dialed.connection, "Wi-Fi", WifiCtx(target, dialed.certFp, paired)) }
            if (running) Thread.sleep(1500)
        }
    }

    private class WifiCtx(val target: SessionHub.WifiTarget, val certFp: ByteArray, val paired: WifiStore.PairedPc?)

    private fun guarded(c: Connection, body: () -> Unit) {
        synchronized(sessionLock) {
            if (busy) {
                c.send(MsgType.BYE, ProtoJson.encodeToString(ByeMsg.serializer(), ByeMsg("busy")).toByteArray())
                Thread.sleep(50)
                c.close()
                return
            }
            busy = true
        }
        try { body() } finally { busy = false }
    }

    // ---- session ----

    private fun runSession(c: Connection, transport: String, wifi: WifiCtx?) {
        conn = c
        dropUntilKey = false
        Log.i(TAG, "PC connected over $transport")

        val cam = CameraController(this,
            onConfig = { json, csd -> c.send(MsgType.VIDEO_CONFIG, configPayload(json, csd), Proto.FLAG_CONFIG) },
            onFrame = ::onVideoFrame,
            onError = { msg -> SessionHub.update { it.copy(error = msg) } },
            onStarted = { w, h, _, _ -> SessionHub.update { it.copy(width = w, height = h, error = null) } })
        camera = cam

        try {
            c.send(MsgType.HELLO, ProtoJson.encodeToString(HelloMsg.serializer(), HelloMsg(
                model = Build.MODEL, manufacturer = Build.MANUFACTURER.replaceFirstChar { it.uppercase() },
                appVersion = APP_VERSION, cameras = cam.describeCameras(),
                phoneId = if (wifi != null) wifiStore.phoneId else null,
                token = wifi?.paired?.token)).toByteArray())
            val pcName = awaitAck(c, wifi)

            val settings = SessionHub.settings.value
            sendState(c, settings)
            SessionHub.update { it.copy(phase = Phase.STREAMING, transport = transport, pcName = pcName, error = null) }
            updateNotification(getString(R.string.notif_streaming_to, pcName))
            cam.start(settings.video)
            startAudio(c, settings.audio)

            sessionJob = scope.launch {
                var prev = settings
                launch { // settings changes from either side
                    SessionHub.settings.collect { next ->
                        if (next == prev) return@collect
                        applySettings(c, prev, next)
                        prev = next
                    }
                }
                launch { SessionHub.previewSurface.collect { cam.setPreview(it) } }
                launch { adaptiveBitrate(c, cam) }
                while (isActive) {
                    delay(2000)
                    sendStats(c)
                }
            }

            while (!c.closed) {
                val p = c.read()
                handle(c, p)
                if (p.type == MsgType.BYE) break
            }
        } catch (e: Exception) {
            Log.i(TAG, "PC link closed: ${e.message}")
            if (e is PairingFailed) SessionHub.update { it.copy(error = e.message) }
        } finally {
            sessionJob?.cancel()
            SessionHub.pairing.value = null
            cam.release()
            audio?.stop()
            audio = null
            camera = null
            c.close()
            conn = null
            SessionHub.update {
                it.copy(phase = if (running) Phase.WAITING_PC else Phase.STOPPED, pcName = null, transport = "", fps = 0.0, kbps = 0, targetKbps = 0)
            }
            if (running) updateNotification(getString(R.string.notif_waiting))
        }
    }

    private class PairingFailed(msg: String) : IOException(msg)

    /**
     * Waits for HELLO_ACK. Over Wi-Fi with an unknown phone the PC first runs pairing:
     * PAIR_BEGIN(commit) → our nonce → PAIR_REVEAL(nonce) → both show the code → confirm.
     */
    private fun awaitAck(c: Connection, wifi: WifiCtx?): String {
        var commit = ""
        var pcName = wifi?.target?.name ?: "PC"
        val myNonce = ByteArray(32).also { SecureRandom().nextBytes(it) }
        while (true) {
            val p = c.read()
            when (p.type) {
                MsgType.HELLO_ACK -> {
                    val ack = runCatching { ProtoJson.decodeFromString(HelloAckMsg.serializer(), p.payload.decodeToString()) }.getOrNull()
                    return ack?.pcName?.ifBlank { null } ?: pcName
                }
                MsgType.BYE -> {
                    val r = runCatching { ProtoJson.decodeFromString(ByeMsg.serializer(), p.payload.decodeToString()).reason }.getOrDefault("")
                    throw PairingFailed("PC refused: $r")
                }
                MsgType.PAIR_BEGIN -> {
                    if (wifi == null) throw PairingFailed("unexpected pairing over USB")
                    val m = ProtoJson.decodeFromString(PairBeginMsg.serializer(), p.payload.decodeToString())
                    commit = m.commit
                    if (m.pcName.isNotBlank()) pcName = m.pcName
                    SessionHub.update { it.copy(phase = Phase.PAIRING) }
                    c.send(MsgType.PAIR_NONCE, ProtoJson.encodeToString(PairNonceMsg.serializer(), PairNonceMsg(Pairing.hex(myNonce))).toByteArray())
                }
                MsgType.PAIR_REVEAL -> {
                    if (wifi == null) throw PairingFailed("unexpected pairing over USB")
                    val m = ProtoJson.decodeFromString(PairNonceMsg.serializer(), p.payload.decodeToString())
                    val pcNonce = Pairing.unhex(m.nonce)
                    var ok = Pairing.commit(pcNonce) == commit // PC can't change its nonce after seeing ours
                    if (ok) {
                        val code = Pairing.sas(wifi.certFp, pcNonce, myNonce)
                        val answer = CompletableDeferred<Boolean>()
                        SessionHub.pairing.value = SessionHub.PairPrompt(pcName, code, answer)
                        ok = runBlocking { withTimeoutOrNull(120_000) { answer.await() } } ?: false
                        SessionHub.pairing.value = null
                    }
                    c.send(MsgType.PAIR_CONFIRM, ProtoJson.encodeToString(PairConfirmMsg.serializer(), PairConfirmMsg(ok)).toByteArray())
                    if (!ok) throw PairingFailed("Pairing cancelled")
                }
                MsgType.PAIR_RESULT -> {
                    val m = ProtoJson.decodeFromString(PairResultMsg.serializer(), p.payload.decodeToString())
                    if (!m.ok || m.token == null || wifi == null) throw PairingFailed("Pairing failed: ${m.reason ?: "rejected"}")
                    wifiStore.savePaired(WifiStore.PairedPc(Pairing.hex(wifi.certFp), pcName, m.token))
                    // Remember the PC by its certificate from now on.
                    val t = wifi.target.copy(name = pcName, shortFp = Pairing.hex(wifi.certFp).take(8))
                    wifiStore.saveTarget(t)
                    SessionHub.wifiTarget.value = t
                }
                else -> Unit
            }
        }
    }

    private fun handle(c: Connection, p: Packet) {
        when (p.type) {
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

    /**
     * Auto bitrate (when the user picked "Auto"): back off fast when the link queues up
     * (Wi-Fi congestion), creep back up when it's clear. The phone's thermal state caps it.
     */
    private suspend fun adaptiveBitrate(c: Connection, cam: CameraController) {
        val pm = getSystemService(PowerManager::class.java)
        var current = 0
        var lastW = 0
        var lastH = 0
        var lastFixed = -1
        var clearSince = System.nanoTime()
        while (true) {
            delay(500)
            val v = SessionHub.settings.value.video
            val (w, h) = cam.encodedSize()
            if (w == 0) continue
            val thermal = pm.currentThermalStatus
            val thermalCap = when {
                thermal >= PowerManager.THERMAL_STATUS_SEVERE -> 0.5
                thermal >= PowerManager.THERMAL_STATUS_MODERATE -> 0.75
                else -> 1.0
            }
            val ceiling = ((if (v.bitrateKbps > 0) v.bitrateKbps else VideoEncoder.autoBitrateKbps(w, h, v.fps)) * thermalCap).toInt()
            if (w != lastW || h != lastH || v.bitrateKbps != lastFixed) { // encoder restarted or user changed it
                current = ceiling
                lastW = w; lastH = h; lastFixed = v.bitrateKbps
            }
            if (v.bitrateKbps > 0 && thermalCap == 1.0) { // fixed rate, phone is cool: leave it alone
                SessionHub.update { it.copy(targetKbps = current, thermal = thermal) }
                continue
            }
            val queued = c.queued()
            var next = current
            when {
                queued > 512 * 1024 -> { next = (current * 0.75).toInt(); clearSince = System.nanoTime() }
                queued > 128 * 1024 -> clearSince = System.nanoTime()
                System.nanoTime() - clearSince > 4_000_000_000L -> { next = (current * 1.1).toInt(); clearSince = System.nanoTime() }
            }
            next = next.coerceIn(500, ceiling.coerceAtLeast(500))
            if (next != current) {
                current = next
                cam.setBitrate(current)
            }
            SessionHub.update { it.copy(targetKbps = current, thermal = thermal) }
        }
    }

    private fun onVideoFrame(data: ByteArray, ptsUs: Long, key: Boolean) {
        val c = conn ?: return
        if (dropUntilKey && !key) return
        if (c.sendVideo(mediaPayload(ptsUs, data), if (key) Proto.FLAG_KEYFRAME else 0)) {
            dropUntilKey = false
            synchronized(this) { framesWindow++; bytesWindow += data.size }
        } else if (!dropUntilKey) {
            dropUntilKey = true // link congested: skip to the next keyframe
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
        SessionHub.pairing.value?.answer?.complete(false)
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
        const val APP_VERSION = "0.2.0"

        fun start(ctx: Context) = ContextCompat.startForegroundService(ctx, Intent(ctx, StreamService::class.java))
        fun stop(ctx: Context) = ctx.startService(Intent(ctx, StreamService::class.java).setAction(ACTION_STOP))
    }
}
